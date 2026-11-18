package gitcli

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hhaidrr/personal-telemetry/internal/ownership"
)

const defaultFetchEvery = time.Minute

// RepoDir is a clone pt owns (a bare one is enough). Revisions it doesn't have
// yet, typically right after a deploy, are fetched from its origin on demand.
type Blamer struct {
	RepoDir        string
	IgnoreRevsFile string
	// Minimum gap between fetches, so a revision that never appears upstream
	// doesn't trigger a fetch for every frame.
	FetchEvery time.Duration

	cache     map[string]map[int]string
	commits   map[string]string
	lastFetch time.Time
}

var _ ownership.Blamer = (*Blamer)(nil)

// Copy detection credits code moved between files to whoever first wrote it,
// while plain blame credits whoever put it in this file. Both are reported.
var strategies = [][]string{{"-w", "-M", "-C"}, {"-w", "-M"}}

func (b *Blamer) Authors(ctx context.Context, rev, path string, line int) ([]string, error) {
	sha, err := b.commit(ctx, rev)
	if err != nil {
		return nil, err
	}
	var authors []string
	for i, flags := range strategies {
		byLine, err := b.lines(ctx, sha, path, i, flags)
		if err != nil {
			return nil, err
		}
		author, ok := byLine[line]
		if !ok {
			return nil, fmt.Errorf("gitcli: %s has no line %d at %s", path, line, sha)
		}
		if !slices.Contains(authors, author) {
			authors = append(authors, author)
		}
	}
	return authors, nil
}

// Hashes are resolved once. Branch names are re-resolved after each fetch so
// a fallback like "main" follows upstream.
func (b *Blamer) commit(ctx context.Context, rev string) (string, error) {
	if sha, ok := b.commits[rev]; ok {
		return sha, nil
	}
	sha, found := b.revParse(ctx, rev)
	var fetchErr error
	if (!found || !isHash(rev)) && time.Since(b.lastFetch) >= or(b.FetchEvery, defaultFetchEvery) {
		b.lastFetch = time.Now()
		if fetchErr = b.git(ctx, "fetch", "--quiet", "--prune", "origin"); fetchErr == nil {
			sha, found = b.revParse(ctx, rev)
		}
	}
	if !found {
		if fetchErr != nil {
			return "", fmt.Errorf("gitcli: %s is not in %s and fetching failed: %w", rev, b.RepoDir, fetchErr)
		}
		return "", fmt.Errorf("gitcli: %s is not in %s, even after fetching", rev, b.RepoDir)
	}
	if isHash(rev) {
		if b.commits == nil {
			b.commits = map[string]string{}
		}
		b.commits[rev] = sha
	}
	return sha, nil
}

func (b *Blamer) revParse(ctx context.Context, rev string) (string, bool) {
	out, err := exec.CommandContext(ctx, "git", "-C", b.RepoDir, "rev-parse", "--verify", "--quiet", rev+"^{commit}").Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

func (b *Blamer) git(ctx context.Context, args ...string) error {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", b.RepoDir}, args...)...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

var hexRev = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

func isHash(rev string) bool { return hexRev.MatchString(rev) }

func or(v, fallback time.Duration) time.Duration {
	if v == 0 {
		return fallback
	}
	return v
}

func (b *Blamer) lines(ctx context.Context, sha, path string, strategy int, flags []string) (map[int]string, error) {
	key := fmt.Sprintf("%d:%s:%s", strategy, sha, path)
	if byLine, ok := b.cache[key]; ok {
		return byLine, nil
	}
	out, err := b.blame(ctx, sha, path, flags)
	if err != nil {
		return nil, err
	}
	byLine, err := parsePorcelain(out)
	if err != nil {
		return nil, err
	}
	if b.cache == nil {
		b.cache = map[string]map[int]string{}
	}
	b.cache[key] = byLine
	return byLine, nil
}

func (b *Blamer) blame(ctx context.Context, sha, path string, flags []string) ([]byte, error) {
	args := append([]string{"-C", b.RepoDir, "blame", "--porcelain"}, flags...)
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
