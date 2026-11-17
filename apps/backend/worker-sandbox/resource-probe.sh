#!/bin/sh
set -eu
read -r mode
case "$mode" in
    output) exec perl -e 'print "x" x (1024 * 1024 + 1)' ;;
    memory) exec perl -e 'my $buffer = "x" x (530 * 1024 * 1024); sleep 10; print "{}" if length($buffer)' ;;
    wall) exec sleep 60 ;;
    *) exit 23 ;;
esac
