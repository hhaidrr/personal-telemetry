package file

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/hhaidrr/personal-telemetry/internal/event"
	"github.com/hhaidrr/personal-telemetry/internal/pipeline"
)

type Source struct {
	Path string
}

var _ pipeline.Source = (*Source)(nil)

func (s *Source) Fetch(ctx context.Context, cursor pipeline.Cursor) ([]event.Event, pipeline.Cursor, error) {
	consumed := 0
	if cursor != "" {
		n, err := strconv.Atoi(string(cursor))
		if err != nil {
			return nil, cursor, fmt.Errorf("file: bad cursor %q", cursor)
		}
		consumed = n
	}

	f, err := os.Open(s.Path)
	if err != nil {
		return nil, cursor, err
	}
	defer f.Close()

	events, lines, err := decode(f, consumed)
	if err != nil {
		return nil, cursor, err
	}
	return events, pipeline.Cursor(strconv.Itoa(lines)), nil
}

func decode(r io.Reader, skip int) ([]event.Event, int, error) {
	var events []event.Event
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		if line <= skip || len(sc.Bytes()) == 0 {
			continue
		}
		var e event.Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return nil, skip, fmt.Errorf("file: line %d: %w", line, err)
		}
		events = append(events, e)
	}
	if err := sc.Err(); err != nil {
		return nil, skip, err
	}
	return events, line, nil
}
