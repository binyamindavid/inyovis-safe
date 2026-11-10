package processing

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const maxXML = 2 << 20

var attachmentLine = regexp.MustCompile(`(?m)^\s*([0-9]+):\s*(.+?)\s*$`)
var decimal = regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?$`)

func ParseIsolated(ctx context.Context, data []byte) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		return Result{}, &ParseFailure{"parser_resource_exhausted", true}
	}
	if configured := os.Getenv("PROCESSING_PARSER_BINARY"); configured != "" {
		executable = configured
	}
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		path, lookup := exec.LookPath("sandbox-exec")
		if lookup != nil {
			return Result{}, &ParseFailure{"parser_resource_exhausted", true}
		}
		command = exec.CommandContext(ctx, path, "-p", "(version 1)(allow default)(deny network*)(deny file-read* (subpath \"/Users\"))(deny file-write* (subpath \"/Users\"))", executable, "--parse")
	case "linux":
		path, lookup := exec.LookPath("bwrap")
		if lookup != nil {
			return Result{}, &ParseFailure{"parser_resource_exhausted", true}
		}
		args := []string{"--die-with-parent", "--unshare-user", "--uid", "0", "--gid", "0", "--unshare-net", "--unshare-pid", "--ro-bind", "/usr", "/usr"}
		for _, directory := range []string{"/bin", "/lib", "/lib64"} {
			if _, statErr := os.Stat(directory); statErr == nil {
				args = append(args, "--ro-bind", directory, directory)
			}
		}
		args = append(args, "--dir", "/app", "--ro-bind", executable, "/app/parser", "--tmpfs", "/tmp", "--proc", "/proc", "--dev", "/dev", "--chdir", "/tmp", "/app/parser", "--parse")
		command = exec.CommandContext(ctx, path, args...)
	default:
		return Result{}, &ParseFailure{"parser_resource_exhausted", true}
	}
	command.Env = []string{"PATH=/usr/bin:/bin:/opt/homebrew/bin", "TMPDIR=/tmp", "HOME=/tmp", "GOMAXPROCS=2"}
	command.Dir = os.TempDir()
	command.Stdin = bytes.NewReader(data)
	var output limitedBuffer
	output.limit = 1 << 20
	command.Stdout = &output
	var stderr limitedBuffer
	stderr.limit = 4096
	command.Stderr = &stderr
	err = runWithMemoryWatch(ctx, command, 512<<20)
	if ctx.Err() != nil {
		return Result{}, &ParseFailure{"parser_timeout", true}
	}
	if output.overflow {
		return Result{}, &ParseFailure{"parser_resource_exhausted", true}
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			switch exit.ExitCode() {
			case 20:
				return Result{}, &ParseFailure{"invalid_pdf", false}
			case 21:
				return Result{}, &ParseFailure{"unsafe_xml", false}
			case 22:
				return Result{}, &ParseFailure{"content_limit_exceeded", false}
			}
		}
		return Result{}, &ParseFailure{"parser_resource_exhausted", true}
	}
	var result Result
	if json.Unmarshal(output.Bytes(), &result) != nil {
		return Result{}, &ParseFailure{"parser_resource_exhausted", true}
	}
	return result, nil
}

func memoryLimitExceeded(rssKiB, limitBytes int64) bool {
	return rssKiB > limitBytes/1024
}

func runWithMemoryWatch(ctx context.Context, command *exec.Cmd, limitBytes int64) error {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
			<-done
			return ctx.Err()
		case <-ticker.C:
			output, err := exec.Command("ps", "-axo", "pid=,ppid=,rss=").Output()
			if err != nil {
				_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
				<-done
				return err
			}
			parents := map[int]int{}
			memory := map[int]int{}
			for _, line := range strings.Split(string(output), "\n") {
				fields := strings.Fields(line)
				if len(fields) != 3 {
					continue
				}
				pid, e1 := strconv.Atoi(fields[0])
				ppid, e2 := strconv.Atoi(fields[1])
				rss, e3 := strconv.Atoi(fields[2])
				if e1 == nil && e2 == nil && e3 == nil {
					parents[pid] = ppid
					memory[pid] = rss
				}
			}
			var totalKiB int64
			for pid, rss := range memory {
				for ancestor := pid; ancestor > 0; ancestor = parents[ancestor] {
					if ancestor == command.Process.Pid {
						totalKiB += int64(rss)
						break
					}
					if parent, ok := parents[ancestor]; !ok || parent == ancestor {
						break
					}
				}
			}
			if memoryLimitExceeded(totalKiB, limitBytes) {
				_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
				<-done
				return &ParseFailure{"parser_resource_exhausted", true}
			}
		}
	}
}

type limitedBuffer struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		b.overflow = true
		return len(p), errors.New("output limit")
	}
	return b.Buffer.Write(p)
}

// ParserMain is entered only by a credential-free, network-isolated child.
func ParserMain() int {
	debug.SetMemoryLimit(384 << 20)
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: 4 << 20, Max: 4 << 20}); err != nil {
		return 23
	}
	if err := syscall.Setrlimit(syscall.RLIMIT_CPU, &syscall.Rlimit{Cur: 20, Max: 20}); err != nil {
		return 23
	}
	data, err := io.ReadAll(io.LimitReader(os.Stdin, (25<<20)+1))
	if err != nil || len(data) > 25<<20 {
		return 22
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	result, err := parsePDF(ctx, data)
	if err != nil {
		var failure *ParseFailure
		if errors.As(err, &failure) {
			switch failure.Code {
			case "invalid_pdf":
				return 20
			case "unsafe_xml":
				return 21
			case "content_limit_exceeded":
				return 22
			}
		}
		return 23
	}
	if err = json.NewEncoder(os.Stdout).Encode(result); err != nil {
		return 23
	}
	return 0
}
func parsePDF(ctx context.Context, data []byte) (Result, error) {
	empty := func(warning string) Result {
		return Result{Candidates: map[string]any{}, Provenance: map[string]any{}, Warnings: []string{warning}}
	}
	if len(data) < 5 || !bytes.HasPrefix(data, []byte("%PDF-")) {
		return Result{}, &ParseFailure{"invalid_pdf", false}
	}
	dir, err := os.MkdirTemp("", "invoyis-parser-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(dir)
	pdf := filepath.Join(dir, "input.pdf")
	if err = os.WriteFile(pdf, data, 0600); err != nil {
		return Result{}, err
	}
	list := exec.CommandContext(ctx, "pdfdetach", "-list", pdf)
	output, err := list.CombinedOutput()
	if err != nil {
		return Result{}, &ParseFailure{"invalid_pdf", false}
	}
	entries := attachmentLine.FindAllStringSubmatch(string(output), -1)
	if len(entries) == 0 {
		return empty("no_supported_xml"), nil
	}
	xmlEntries := 0
	canonical := 0
	for _, entry := range entries {
		if strings.HasSuffix(strings.ToLower(entry[2]), ".xml") {
			xmlEntries++
			if entry[2] == "factur-x.xml" {
				canonical++
			}
		}
	}
	if xmlEntries > 1 || canonical > 1 {
		return empty("ambiguous_invoice_xml"), nil
	}
	if canonical != 1 {
		return empty("no_supported_xml"), nil
	}
	dest := filepath.Join(dir, "invoice.xml")
	extract := exec.CommandContext(ctx, "pdfdetach", "-savefile", "factur-x.xml", "-o", dest, pdf)
	if err = extract.Run(); err != nil {
		if stat, statErr := os.Stat(dest); statErr == nil && stat.Size() >= maxXML {
			return Result{}, &ParseFailure{"content_limit_exceeded", false}
		}
		return Result{}, &ParseFailure{"invalid_pdf", false}
	}
	stat, err := os.Stat(dest)
	if err != nil {
		return Result{}, err
	}
	if stat.Size() > maxXML {
		return Result{}, &ParseFailure{"content_limit_exceeded", false}
	}
	xmlData, err := os.ReadFile(dest)
	if err != nil {
		return Result{}, err
	}
	result, err := parseXML(xmlData)
	if err != nil {
		return Result{}, err
	}
	return result, nil
}
func parseXML(data []byte) (Result, error) {
	if len(data) > maxXML {
		return Result{}, &ParseFailure{"content_limit_exceeded", false}
	}
	result := Result{Candidates: map[string]any{}, Provenance: map[string]any{}, Warnings: []string{}}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = true
	decoder.Entity = map[string]string{}
	var path []string
	var taxLines []string
	var currencies []string
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Result{}, &ParseFailure{"unsafe_xml", false}
		}
		switch item := token.(type) {
		case xml.Directive:
			return Result{}, &ParseFailure{"unsafe_xml", false}
		case xml.StartElement:
			path = append(path, item.Name.Local)
			if len(path) > 80 {
				return Result{}, &ParseFailure{"content_limit_exceeded", false}
			}
		case xml.EndElement:
			if len(path) > 0 {
				path = path[:len(path)-1]
			}
		case xml.CharData:
			value := strings.TrimSpace(string(item))
			if value == "" {
				continue
			}
			if len(value) > 4096 {
				return Result{}, &ParseFailure{"content_limit_exceeded", false}
			}
			full := "/" + strings.Join(path, "/")
			last := func(parts ...string) bool {
				if len(path) < len(parts) {
					return false
				}
				for i, p := range parts {
					if path[len(path)-len(parts)+i] != p {
						return false
					}
				}
				return true
			}
			set := func(field string) {
				if old, exists := result.Candidates[field]; exists && old != value {
					result.Warnings = append(result.Warnings, "conflicting_"+field)
				} else if !exists {
					result.Candidates[field] = value
					result.Provenance[field] = full
				}
			}
			switch {
			case last("GuidelineSpecifiedDocumentContextParameter", "ID"):
				lower := strings.ToLower(value)
				switch {
				case strings.Contains(lower, "basicwl"):
					result.Profile = "basic_wl"
				case strings.Contains(lower, "minimum"):
					result.Profile = "minimum"
				case strings.Contains(lower, "extended"):
					result.Profile = "extended"
				case strings.Contains(lower, "en16931"):
					result.Profile = "en_16931"
				case strings.Contains(lower, "basic"):
					result.Profile = "basic"
				}
			case last("SellerTradeParty", "Name"):
				set("supplier")
			case last("ExchangedDocument", "ID"):
				set("invoiceId")
			case last("IssueDateTime", "DateTimeString"):
				set("issueDate")
			case last("SupplyChainTradeTransaction", "ApplicableHeaderTradeSettlement", "InvoiceCurrencyCode"):
				set("currency")
				currencies = append(currencies, value)
			case last("SupplyChainTradeTransaction", "ApplicableHeaderTradeSettlement", "SpecifiedTradeSettlementHeaderMonetarySummation", "LineTotalAmount"):
				if decimal.MatchString(value) {
					set("subtotal")
				}
			case last("SupplyChainTradeTransaction", "ApplicableHeaderTradeSettlement", "SpecifiedTradeSettlementHeaderMonetarySummation", "TaxTotalAmount"):
				if decimal.MatchString(value) {
					set("tax")
				}
			case last("SupplyChainTradeTransaction", "ApplicableHeaderTradeSettlement", "SpecifiedTradeSettlementHeaderMonetarySummation", "GrandTotalAmount"):
				if decimal.MatchString(value) {
					set("total")
				}
			case last("SupplyChainTradeTransaction", "ApplicableHeaderTradeSettlement", "ApplicableTradeTax", "CalculatedAmount"):
				if decimal.MatchString(value) {
					taxLines = append(taxLines, value)
				}
			}
		}
	}
	if result.Profile == "" {
		return Result{Candidates: map[string]any{}, Provenance: map[string]any{}, Warnings: []string{"unsupported_profile"}}, nil
	}
	if len(taxLines) > 0 {
		result.Candidates["taxLines"] = taxLines
		result.Provenance["taxLines"] = "/CrossIndustryInvoice/SupplyChainTradeTransaction/ApplicableHeaderTradeSettlement/ApplicableTradeTax/CalculatedAmount"
	}
	for _, currency := range currencies {
		if currency != result.Candidates["currency"] {
			result.Warnings = append(result.Warnings, "inconsistent_currency")
			break
		}
	}
	if subtotal, ok := result.Candidates["subtotal"].(string); ok {
		if tax, ok := result.Candidates["tax"].(string); ok {
			if total, ok := result.Candidates["total"].(string); ok {
				a, aok := new(big.Rat).SetString(subtotal)
				b, bok := new(big.Rat).SetString(tax)
				c, cok := new(big.Rat).SetString(total)
				if aok && bok && cok && new(big.Rat).Add(a, b).Cmp(c) != 0 {
					result.Warnings = append(result.Warnings, "inconsistent_total")
				}
			}
		}
	}
	return result, nil
}
