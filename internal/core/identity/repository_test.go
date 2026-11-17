package identity

import "database/sql"

func identityDB(s *Service) *sql.DB { return s.store.(*sqliteRepository).db }
