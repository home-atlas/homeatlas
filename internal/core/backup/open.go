package backup

import (
	"context"
	"os"
)

// ContextFiles supports remote originals with cancellation and temporary-file cleanup.
type ContextFiles interface {
	OpenFileContext(context.Context, string) (*os.File, func(), error)
}

func OpenOriginal(ctx context.Context, p Provider, key string) (*os.File, func(), error) {
	if remote, ok := p.(ContextFiles); ok {
		return remote.OpenFileContext(ctx, key)
	}
	file, err := p.OpenFile(key)
	return file, func() {
		if file != nil {
			file.Close()
		}
	}, err
}
