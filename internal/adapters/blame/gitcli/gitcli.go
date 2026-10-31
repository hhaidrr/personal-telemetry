package gitcli

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/hhaidrr/personal-telemetry/internal/ownership"
)

type Blamer struct {
	RepoDir        string
	IgnoreRevsFile string

	cache map[string]map[int]string
}

var _ ownership.Blamer = (*Blamer)(nil)

func (b *Blamer) Author(ctx context.Context, sha, path string, line int) (string, error) {
	key := sha + ":" + path
	authors, ok := b.cache[key]
	if !ok {
		out, err := b.blame(ctx, sha, path)
		if err != nil {
			return "", err
		}
		if authors, err = parsePorcelain(out); err != nil {
			return "", err
		}
		if b.cache == nil {
			b.cache = map[string]map[int]string{}
		}
		b.cache[key] = authors
	}
	author, ok := authors[line]
	if !ok {
		return "", fmt.Errorf("gitcli: %s has no line %d at %s", path, line, sha)
	}
	return author, nil
}

func (b *Blamer) blame(ctx context.Context, sha, path string) ([]byte, error) {
	args := []string{"-C", b.RepoDir, "blame", "--porcelain", "-w", "-M", "-C"}
	if b.IgnoreRevsFile != "" {
		args = append(args, "--ignore-revs-file", b.IgnoreRevsFile)
	}
	args = append(args, sha, "--", path)

	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gitcli: git blame %s %s: %w: %s", sha, path, err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// Porcelain prints a commit's author headers only the first time the commit
// appears, so emails are remembered per commit and reused for later lines.
func parsePorcelain(out []byte) (map[int]string, error) {
	authors := map[int]string{}
	emailByCommit := map[string]string{}
	var commit string
	var finalLine int

	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		text := sc.Text()
		switch {
		case strings.HasPrefix(text, "\t"):
			authors[finalLine] = emailByCommit[commit]
		case strings.HasPrefix(text, "author-mail "):
			email := strings.Trim(strings.TrimPrefix(text, "author-mail "), "<>")
			emailByCommit[commit] = strings.ToLower(email)
		case isHeader(text):
			fields := strings.Fields(text)
			n, err := strconv.Atoi(fields[2])
			if err != nil {
				return nil, fmt.Errorf("gitcli: bad porcelain header %q", text)
			}
			commit, finalLine = fields[0], n
		}
	}
	return authors, sc.Err()
}

func isHeader(text string) bool {
	fields := strings.Fields(text)
	if len(fields) < 3 || len(fields[0]) != 40 {
		return false
	}
	_, err := strconv.ParseUint(fields[0][:16], 16, 64)
	return err == nil
}
