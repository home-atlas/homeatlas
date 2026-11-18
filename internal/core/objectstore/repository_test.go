package objectstore

import (
	"context"
	"encoding/json"

	"github.com/home-atlas/homeatlas/internal/core/objectstore/adapters/localcrypto"
	"github.com/home-atlas/homeatlas/internal/core/objectstore/adapters/sqlite"
)

func testProfile(s *Store, ctx context.Context, id string) (string, S3Config, error) {
	home, sealed, err := sqlite.New(s.db).Profile(ctx, id)
	if err != nil {
		return home, S3Config{}, err
	}
	raw, err := localcrypto.New(s.dir).Crypt(home, id, sealed, false)
	if err != nil {
		return home, S3Config{}, err
	}
	var config S3Config
	err = json.Unmarshal(raw, &config)
	return home, config, err
}
func testCurrent(s *Store, ctx context.Context, home string) (Settings, string, error) {
	settings, err := s.Settings(ctx, home)
	id, _, readErr := sqlite.New(s.db).Settings(ctx, nil, home)
	if err == nil {
		err = readErr
	}
	return settings, id, err
}
