package sqlite

import (
	"github.com/home-atlas/homeatlas/internal/core/collections/application"
	"github.com/home-atlas/homeatlas/internal/core/database"
)

type migrationConfig struct{ config Config }

func Migrations(config application.Config) []database.Migration {
	return (&migrationConfig{Config{config}}).Migrations()
}
func (m *migrationConfig) Migrations() []database.Migration {
	migrations := []database.Migration{{Version: 1, SQL: `
		CREATE TABLE collection_items (
			id TEXT PRIMARY KEY, household_id TEXT NOT NULL, owner_id TEXT NOT NULL,
			title TEXT NOT NULL, category TEXT NOT NULL DEFAULT '__collection_default__',
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')));
		CREATE INDEX collection_access ON collection_items (household_id, owner_id);
		CREATE VIRTUAL TABLE collection_fts USING fts5(title, content='collection_items', content_rowid='rowid');
		CREATE TRIGGER collection_ai AFTER INSERT ON collection_items BEGIN
			INSERT INTO collection_fts(rowid, title) VALUES (new.rowid, new.title); END;
		CREATE TRIGGER collection_ad AFTER DELETE ON collection_items BEGIN
			INSERT INTO collection_fts(collection_fts, rowid, title) VALUES ('delete', old.rowid, old.title); END;
		CREATE TRIGGER collection_au AFTER UPDATE ON collection_items BEGIN
			INSERT INTO collection_fts(collection_fts, rowid, title) VALUES ('delete', old.rowid, old.title);
			INSERT INTO collection_fts(rowid, title) VALUES (new.rowid, new.title); END;`},
		{Version: 2, SQL: `
		ALTER TABLE collection_items ADD COLUMN description TEXT NOT NULL DEFAULT '';
		ALTER TABLE collection_items ADD COLUMN tags TEXT NOT NULL DEFAULT '[]';
		ALTER TABLE collection_items ADD COLUMN original_filename TEXT NOT NULL DEFAULT '';
		ALTER TABLE collection_items ADD COLUMN media_type TEXT NOT NULL DEFAULT '';
		ALTER TABLE collection_items ADD COLUMN size_bytes INTEGER NOT NULL DEFAULT 0 CHECK (size_bytes >= 0);
		ALTER TABLE collection_items ADD COLUMN sha256 TEXT NOT NULL DEFAULT '';
		ALTER TABLE collection_items ADD COLUMN file_key TEXT NOT NULL DEFAULT '';
		CREATE UNIQUE INDEX collection_file_key ON collection_items(file_key) WHERE file_key != '';
		DROP TRIGGER collection_ai;
		DROP TRIGGER collection_ad;
		DROP TRIGGER collection_au;
		DROP TABLE collection_fts;
		CREATE VIRTUAL TABLE collection_fts USING fts5(title, description, tags, content='collection_items', content_rowid='rowid');
		CREATE TRIGGER collection_ai AFTER INSERT ON collection_items BEGIN
			INSERT INTO collection_fts(rowid, title, description, tags) VALUES(new.rowid, new.title, new.description, new.tags); END;
		CREATE TRIGGER collection_ad AFTER DELETE ON collection_items BEGIN
			INSERT INTO collection_fts(collection_fts, rowid, title, description, tags) VALUES('delete', old.rowid, old.title, old.description, old.tags); END;
		CREATE TRIGGER collection_au AFTER UPDATE ON collection_items BEGIN
			INSERT INTO collection_fts(collection_fts, rowid, title, description, tags) VALUES('delete', old.rowid, old.title, old.description, old.tags);
			INSERT INTO collection_fts(rowid, title, description, tags) VALUES(new.rowid, new.title, new.description, new.tags); END;
		INSERT INTO collection_fts(collection_fts) VALUES('rebuild');`},
		{Version: 3, SQL: `
 CREATE TABLE collection_grants (
   item_id TEXT NOT NULL REFERENCES collection_items(id) ON DELETE CASCADE,
   household_id TEXT NOT NULL, user_id TEXT NOT NULL, member_id TEXT NOT NULL,
   created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
   PRIMARY KEY(item_id,user_id));
 CREATE INDEX collection_grants_recipient ON collection_grants(household_id,member_id,item_id);`}}
	for i := range migrations {
		migrations[i].SQL = m.config.sql(migrations[i].SQL)
	}
	return migrations
}
