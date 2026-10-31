package gcs

import (
	"context"
	"errors"
)

type StateFile struct {
	Bucket string
	Object string
}

func (s *StateFile) Pull(ctx context.Context, dbPath string) error {
	return errors.New("gcs: Pull not implemented")
}

func (s *StateFile) Push(ctx context.Context, dbPath string) error {
	return errors.New("gcs: Push not implemented")
}
