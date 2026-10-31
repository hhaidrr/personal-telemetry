package local

import "context"

type StateFile struct{}

func (StateFile) Pull(ctx context.Context, dbPath string) error { return nil }
func (StateFile) Push(ctx context.Context, dbPath string) error { return nil }
