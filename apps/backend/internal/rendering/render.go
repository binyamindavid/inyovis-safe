package rendering

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const RendererVersion = "poppler-png-v1"

var pageCountPattern = regexp.MustCompile(`(?m)^Pages:\s+(\d+)\s*$`)
var pageSizePattern = regexp.MustCompile(`(?m)^Page(?:\s+\d+)? size:\s+([0-9.]+) x ([0-9.]+) pts`)
var ErrPreviewUnsupported = errors.New("preview unsupported")
var ErrPreviewTooLarge = errors.New("preview page too large")
var ErrPreviewTimeout = errors.New("preview renderer timed out")

// RenderIsolated supplies only verified PDF bytes to a credential-free sandbox.
func RenderIsolated(ctx context.Context, data []byte, page int) ([]byte, int, error) {
	if page < 1 || page > 10 || len(data) > 25<<20 {
		return nil, 0, ErrPreviewTooLarge
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		return nil, 0, err
	}
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		sandbox, err := exec.LookPath("sandbox-exec")
		if err != nil {
			return nil, 0, err
		}
		command = exec.CommandContext(ctx, sandbox, "-p", "(version 1)(allow default)(deny network*)(deny file-read* (subpath \"/Users\"))(deny file-write* (subpath \"/Users\"))", executable, "--render-page", strconv.Itoa(page))
	case "linux":
		bwrap, err := exec.LookPath("bwrap")
		if err != nil {
			return nil, 0, err
		}
		args := []string{"--die-with-parent", "--unshare-user", "--uid", "0", "--gid", "0", "--unshare-net", "--unshare-pid", "--ro-bind", "/usr", "/usr"}
		for _, dir := range []string{"/bin", "/lib", "/lib64"} {
			if _, e := os.Stat(dir); e == nil {
				args = append(args, "--ro-bind", dir, dir)
			}
		}
		args = append(args, "--dir", "/app", "--ro-bind", executable, "/app/renderer", "--tmpfs", "/tmp", "--proc", "/proc", "--dev", "/dev", "--chdir", "/tmp", "/app/renderer", "--render-page", strconv.Itoa(page))
		command = exec.CommandContext(ctx, bwrap, args...)
	default:
		return nil, 0, ErrPreviewUnsupported
	}
	command.Env = []string{"PATH=/usr/bin:/bin:/opt/homebrew/bin", "TMPDIR=/tmp", "HOME=/tmp", "GOMAXPROCS=2"}
	command.Dir = os.TempDir()
	command.Stdin = bytes.NewReader(data)
	var output limitedBuffer
	output.limit = 4_100_000
	command.Stdout = &output
	var stderr limitedBuffer
	stderr.limit = 1024
	command.Stderr = &stderr
	err = runWithMemoryWatch(ctx, command, 512<<20)
	if ctx.Err() != nil {
		return nil, 0, ErrPreviewTimeout
	}
	if output.overflow {
		return nil, 0, ErrPreviewTooLarge
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			switch exit.ExitCode() {
			case 20:
				return nil, 0, ErrPreviewUnsupported
			case 22:
				return nil, 0, ErrPreviewTooLarge
			}
		}
		return nil, 0, ErrPreviewUnsupported
	}
	raw := output.Bytes()
	index := bytes.IndexByte(raw, '\n')
	if index < 1 || index > 3 {
		return nil, 0, ErrPreviewUnsupported
	}
	count, err := strconv.Atoi(string(raw[:index]))
	if err != nil || count < 1 || count > 10000 {
		return nil, 0, ErrPreviewUnsupported
	}
	image := raw[index+1:]
	if len(image) > 4_000_000 || !bytes.HasPrefix(image, []byte("\x89PNG\r\n\x1a\n")) {
		return nil, 0, ErrPreviewUnsupported
	}
	return image, count, nil
}
func RenderMain(page int) int {
	if page < 1 || page > 10 {
		return 22
	}
	_ = syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: 5 << 20, Max: 5 << 20})
	_ = syscall.Setrlimit(syscall.RLIMIT_CPU, &syscall.Rlimit{Cur: 10, Max: 10})
	data, err := io.ReadAll(io.LimitReader(os.Stdin, (25<<20)+1))
	if err != nil || len(data) > 25<<20 {
		return 22
	}
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		return 20
	}
	dir, err := os.MkdirTemp("", "invoyis-render-")
	if err != nil {
		return 22
	}
	defer os.RemoveAll(dir)
	input := filepath.Join(dir, "input.pdf")
	if os.WriteFile(input, data, 0600) != nil {
		return 22
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	info, err := exec.CommandContext(ctx, "pdfinfo", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), input).Output()
	if err != nil {
		return 20
	}
	match := pageCountPattern.FindSubmatch(info)
	if len(match) != 2 {
		return 20
	}
	count, _ := strconv.Atoi(string(match[1]))
	if page > count {
		return 20
	}
	size := pageSizePattern.FindSubmatch(info)
	if len(size) == 3 {
		width, _ := strconv.ParseFloat(string(size[1]), 64)
		height, _ := strconv.ParseFloat(string(size[2]), 64)
		if width > 20000 || height > 20000 || width < 1 || height < 1 {
			return 22
		}
	}
	output := filepath.Join(dir, "page")
	command := exec.CommandContext(ctx, "pdftoppm", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), "-singlefile", "-scale-to", "1600", "-png", input, output)
	if command.Run() != nil {
		return 20
	}
	imageBytes, err := os.ReadFile(output + ".png")
	if err != nil || len(imageBytes) > 4_000_000 || len(imageBytes) < 24 || !bytes.HasPrefix(imageBytes, []byte("\x89PNG\r\n\x1a\n")) {
		return 22
	}
	width := binary.BigEndian.Uint32(imageBytes[16:20])
	height := binary.BigEndian.Uint32(imageBytes[20:24])
	if width < 1 || height < 1 || width > 1600 || height > 1600 {
		return 22
	}
	if _, err = fmt.Fprintf(os.Stdout, "%d\n", count); err != nil {
		return 22
	}
	if _, err = os.Stdout.Write(imageBytes); err != nil {
		return 22
	}
	return 0
}
func PreviewFailureCode(err error) string {
	switch {
	case errors.Is(err, ErrPreviewTooLarge):
		return "PREVIEW_TOO_LARGE"
	case errors.Is(err, ErrPreviewTimeout):
		return "PREVIEW_TIMEOUT"
	default:
		return "PREVIEW_UNAVAILABLE"
	}
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
			if totalKiB > limitBytes/1024 {
				_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
				<-done
				return errors.New("renderer memory limit")
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
