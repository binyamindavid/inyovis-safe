#!/bin/sh
# Run in the worker image, using synthetic input and no database or S3.
set -eu

smoke=/opt/invoyis/smoke
scratch=$(mktemp -d /dev/shm/invoyis-smoke.XXXXXXXX)
secret=$(mktemp)
trap 'rm -rf "$scratch" "$secret"' EXIT
printf '%s\n' synthetic-secret > "$secret"

checks='
use strict;
my ($secret, $mode, $program) = @ARGV;
die "secret readable\n" if open(my $outside, "<", $secret);
die "network socket available\n" if syscall(41, 2, 1, 0) >= 0;
for my $limit ([0, 20], [1, 25 * 1024 * 1024]) {
    my $values = pack("QQ", 0, 0);
    die "resource limit not installed\n"
        if syscall(97, $limit->[0], $values) || (unpack("QQ", $values))[1] != $limit->[1];
}
die "credential inherited\n" if exists $ENV{DOCUMENT_WORKER_DATABASE_URL};
if ($mode eq "parent") {
    die "scratch unwritable\n" unless open(my $inside, ">", "$ENV{TMPDIR}/writable");
    die "subprocess isolation failed\n" unless system($^X, "-e", $program, $secret, "child", $program) == 0;
}
'
DOCUMENT_WORKER_DATABASE_URL=synthetic env -i PATH=/usr/bin:/bin TMPDIR="$scratch" \
    /usr/local/bin/invoyis-parser-sandbox "$scratch" /usr/bin/perl \
    -e "$checks" "$secret" parent "$checks"
echo "Filesystem and network isolation passed in parent and subprocess"

digital=$(document-worker --synthetic-pdf-check "$smoke/digital.pdf")
printf '%s\n' "$digital" | grep -q '"invoiceId":"INV00001234"'
printf '%s\n' "$digital" | grep -q '"source":"digital"'
echo "Digital PDF extraction passed"

scanned=$(document-worker --synthetic-pdf-check "$smoke/scanned.pdf")
printf '%s\n' "$scanned" | grep -q '"invoiceId":"00123456"'
printf '%s\n' "$scanned" | grep -q '"source":"ocr"'
echo "Scanned PDF OCR passed"

# Invoke the same bounded parent used for production parsing with harmless
# synthetic children that attempt to exceed each limit.
probe="$smoke/resource-probe.sh"
check_limit() {
    mode=$1 expected=$2
    printf '%s\n' "$mode" > "$scratch/input"
    if result=$(PROCESSING_PARSER_BINARY="$probe" document-worker --synthetic-pdf-check "$scratch/input" 2>&1); then
        echo "Sandbox accepted the $mode resource probe" >&2
        exit 1
    fi
    printf '%s\n' "$result" | grep -q "$expected"
    echo "$mode limit enforced"
}

check_limit output parser_resource_exhausted
check_limit memory parser_resource_exhausted
check_limit wall parser_timeout
