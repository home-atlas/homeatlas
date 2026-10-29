package app

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/backup"
	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/identity"
)

var moduleID = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func (b *householdBackups) validateParticipants() error {
	seen := map[string]bool{}
	legacy := false
	for _, p := range b.participants {
		if p.Provider == nil || !moduleID.MatchString(p.ID()) || p.Version() < 1 || p.MaxFileBytes() < 1 || p.MaxFileBytes() > maxBackupBytes || seen[p.ID()] {
			return fmt.Errorf("invalid backup participant")
		}
		if p.LegacyPrefix != "" {
			if legacy || !moduleID.MatchString(p.LegacyPrefix) {
				return fmt.Errorf("invalid legacy backup adapter")
			}
			legacy = true
		}
		seen[p.ID()] = true
	}
	return nil
}
func (b *householdBackups) participant(id string) (backup.Participant, bool) {
	for _, p := range b.participants {
		if p.ID() == id {
			return p, true
		}
	}
	return backup.Participant{}, false
}
func (b *householdBackups) legacy() (backup.Participant, bool) {
	for _, p := range b.participants {
		if p.LegacyPrefix != "" {
			if v, ok := p.Provider.(interface {
				ProviderForVersion(int) (backup.Provider, error)
			}); ok {
				provider, err := v.ProviderForVersion(1)
				if err != nil {
					return backup.Participant{}, false
				}
				p.Provider = provider
			}
			return p, true
		}
	}
	return backup.Participant{}, false
}
func (b *householdBackups) snapshotParticipant(part snapshotPart) (backup.Participant, bool) {
	p, ok := b.participant(part.ID)
	if !ok {
		return p, false
	}
	if p.Version() == part.Version {
		return p, true
	}
	if versions, ok := p.Provider.(interface {
		ProviderForVersion(int) (backup.Provider, error)
	}); ok {
		provider, err := versions.ProviderForVersion(part.Version)
		if err == nil {
			p.Provider = provider
			return p, true
		}
	}
	return backup.Participant{}, false
}
func (b *householdBackups) partPrefix(version int, p backup.Participant) string {
	if version < 4 {
		return p.LegacyPrefix
	}
	return "modules/" + p.ID()
}
func (b *householdBackups) profile(s householdSnapshot) ([]backup.Participant, error) {
	if s.version < 4 {
		p, ok := b.legacy()
		if !ok {
			return nil, errBackupModuleRequired
		}
		return []backup.Participant{p}, nil
	}
	result := []backup.Participant{}
	for _, part := range s.parts {
		p, ok := b.snapshotParticipant(part)
		if !ok || p.Version() != part.Version {
			return nil, errBackupModuleRequired
		}
		result = append(result, p)
	}
	return result, nil
}
func (b *householdBackups) schemas(s householdSnapshot) ([]string, error) {
	result := append([]string{}, backupSchema...)
	if s.version >= 3 {
		result = append(result, backupMembersSchemaV3)
	} else if s.version == 2 {
		result = append(result, backupMembersSchema)
	}
	profile, err := b.profile(s)
	if err != nil {
		return nil, err
	}
	for _, p := range profile {
		result = append(result, p.Tables()...)
	}
	return result, nil
}
func readCore(ctx context.Context, q backup.Queryer, live bool) (householdSnapshot, error) {
	s := householdSnapshot{modules: map[string]bool{}}
	principal, ok := access.Current(ctx)
	p := principal.HouseholdID
	homeSQL := "SELECT id,name,timezone FROM household"
	modSQL := "SELECT id,enabled FROM modules"
	var args []any
	if live {
		if !ok {
			return s, errBackupDenied
		}
		homeSQL = "SELECT id,name,timezone FROM identity_households WHERE id=?"
		modSQL = "SELECT module_id,enabled FROM household_module_states WHERE household_id=?"
		args = []any{p}
	}
	if err := q.QueryRowContext(ctx, homeSQL, args...).Scan(&s.household.ID, &s.household.Name, &s.household.Timezone); err != nil {
		return s, err
	}
	rows, err := q.QueryContext(ctx, modSQL, args...)
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var enabled bool
		if err := rows.Scan(&id, &enabled); err != nil {
			return s, err
		}
		s.modules[id] = enabled
		if len(s.modules) > 1000 {
			return s, errInvalidBackup
		}
	}
	return s, rows.Err()
}
func writeCore(ctx context.Context, tx *sql.Tx, s householdSnapshot) error {
	if _, err := tx.ExecContext(ctx, "INSERT INTO household VALUES(?,?,?)", s.household.ID, s.household.Name, s.household.Timezone); err != nil {
		return err
	}
	for id, enabled := range s.modules {
		if _, err := tx.ExecContext(ctx, "INSERT INTO modules VALUES(?,?)", id, enabled); err != nil {
			return err
		}
	}
	if s.version < 2 {
		return nil
	}
	for _, m := range s.members {
		permissions, _ := json.Marshal(m.Permissions)
		query := "INSERT INTO members VALUES(?,?,?,?,?,?,?)"
		args := []any{m.ID, m.Name, m.Email, m.PasswordHash, m.Role, m.MembershipID, string(permissions)}
		if s.version >= 3 {
			query = "INSERT INTO members VALUES(?,?,?,?,?,?,?,?,?)"
			args = append(args, m.Username, m.Assisted)
		}
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}
	return nil
}
func readMembers(ctx context.Context, q backup.Queryer, version int) ([]identity.BackupMember, error) {
	if version < 2 {
		return nil, nil
	}
	query := "SELECT id,name,email,password_hash,role,member_id,permissions"
	if version >= 3 {
		query += ",username,assisted"
	}
	query += " FROM members"
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	members := []identity.BackupMember{}
	for rows.Next() {
		var m identity.BackupMember
		var permissions string
		args := []any{&m.ID, &m.Name, &m.Email, &m.PasswordHash, &m.Role, &m.MembershipID, &permissions}
		if version >= 3 {
			args = append(args, &m.Username, &m.Assisted)
		}
		if err = rows.Scan(args...); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(permissions), &m.Permissions); err != nil {
			break
		}
		members = append(members, m)
		if len(members) > 1000 {
			err = errInvalidBackup
			break
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	return members, identity.ValidateBackupMembers(members)
}
func hashCopy(dst io.Writer, src io.Reader, limit int64) (string, int64, error) {
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, h), io.LimitReader(src, limit+1))
	if err == nil && n > limit {
		err = errInvalidBackup
	}
	return hex.EncodeToString(h.Sum(nil)), n, err
}
func openSnapshot(path string) (*sql.DB, error) {
	dsn := (&url.URL{Scheme: "file", Path: path}).String() + "?mode=ro&immutable=1&_pragma=trusted_schema(0)"
	db, err := sql.Open("sqlite", dsn)
	if err == nil {
		db.SetMaxOpenConns(1)
	}
	return db, err
}
func validateContents(c backup.Contents) error {
	if c.Records < 0 || len(c.Files) > maxBackupFiles || len(c.Unavailable) > maxBackupFiles {
		return errInvalidBackup
	}
	seen := map[string]bool{}
	for _, f := range c.Files {
		hash, err := hex.DecodeString(f.SHA256)
		if !backupKey.MatchString(f.Key) || seen[f.Key] || err != nil || len(hash) != 32 || f.Size <= 0 || f.Size > maxBackupBytes {
			return errInvalidBackup
		}
		seen[f.Key] = true
	}
	seen = map[string]bool{}
	for _, id := range c.Unavailable {
		if !backupKey.MatchString(id) || seen[id] {
			return errInvalidBackup
		}
		seen[id] = true
	}
	return nil
}
func validateCore(s householdSnapshot, household string) error {
	if s.household.ID != household || !backupKey.MatchString(household) || !backupText(s.household.Name, 100) || strings.TrimSpace(s.household.Name) == "" || s.household.Timezone == "Local" || s.household.Timezone == "" {
		return errInvalidBackup
	}
	if _, err := time.LoadLocation(s.household.Timezone); err != nil {
		return errInvalidBackup
	}
	ids := map[string]bool{}
	for _, part := range s.parts {
		if !moduleID.MatchString(part.ID) || part.Version < 1 || ids[part.ID] {
			return errInvalidBackup
		}
		ids[part.ID] = true
		if err := validateContents(part.contents); err != nil {
			return err
		}
	}
	for id := range s.modules {
		if !ids[id] {
			return errInvalidBackup
		}
	}
	if s.coreOnly && len(s.parts) > 0 {
		return errInvalidBackup
	}
	return nil
}
func backupText(v string, n int) bool {
	if !utf8.ValidString(v) || utf8.RuneCountInString(v) > n {
		return false
	}
	for _, c := range v {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}

func (b *householdBackups) create(ctx context.Context, dir string, recoverable bool, version int) (string, int, error) {
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback()
	principal, err := b.authorize(ctx, tx)
	if err != nil {
		return "", 0, err
	}
	s, err := readCore(ctx, tx, true)
	if err != nil {
		return "", 0, err
	}
	s.version = version
	// Installed data without a provider must never disappear silently from an export.
	rows, err := tx.QueryContext(ctx, "SELECT id FROM module_states")
	if err != nil {
		return "", 0, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return "", 0, err
	}
	for _, id := range ids {
		if _, ok := b.participant(id); !ok {
			return "", 0, errBackupModuleRequired
		}
	}
	for _, p := range b.participants {
		present, err := p.Present(ctx, tx)
		if err != nil {
			return "", 0, err
		}
		if !present {
			if slices.Contains(ids, p.ID()) {
				return "", 0, errInvalidBackup
			}
			continue
		}
		s.parts = append(s.parts, snapshotPart{ID: p.ID(), Version: p.Version()})
		if s.version < 4 && (p.LegacyPrefix == "" || p.Version() > 1) {
			if s.version < 2 {
				return "", 0, errBackupModuleRequired
			}
			s.version = 4
		}
	}
	s.coreOnly = len(s.parts) == 0
	if s.version >= 2 {
		s.members, err = b.accounts.BackupMembers(ctx, tx, principal.HouseholdID)
		if err != nil {
			return "", 0, err
		}
		for _, m := range s.members {
			if s.version < 3 && (m.Email == "" || m.Assisted || m.Username != "u-"+strings.ToLower(m.ID)) {
				s.version = 3
			}
		}
	}
	schema, err := b.schemas(s)
	if err != nil {
		return "", 0, err
	}
	path := filepath.Join(dir, "household.sqlite")
	snapshot, err := database.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer snapshot.Close()
	if _, err = snapshot.ExecContext(ctx, "PRAGMA journal_mode=DELETE"); err != nil {
		return "", 0, err
	}
	out, err := snapshot.BeginTx(ctx, nil)
	if err != nil {
		return "", 0, err
	}
	defer out.Rollback()
	for _, ddl := range schema {
		if _, err = out.ExecContext(ctx, ddl); err != nil {
			return "", 0, err
		}
	}
	if err = writeCore(ctx, out, s); err != nil {
		return "", 0, err
	}
	for _, part := range s.parts {
		p, _ := b.snapshotParticipant(part)
		if err = p.Export(ctx, tx, out, s.household.ID, recoverable); err != nil {
			return "", 0, err
		}
	}
	if err = out.Commit(); err != nil {
		return "", 0, err
	}
	if err = tx.Commit(); err != nil {
		return "", 0, err
	}
	for i, part := range s.parts {
		p, _ := b.snapshotParticipant(part)
		s.parts[i].contents, err = p.Inspect(ctx, snapshot, s.household.ID)
		if err != nil {
			return "", 0, err
		}
	}
	if err = validateCore(s, principal.HouseholdID); err != nil {
		return "", 0, err
	}
	if err = snapshot.Close(); err != nil {
		return "", 0, err
	}
	return b.writeArchive(ctx, dir, path, s)
}
func (b *householdBackups) missing(s householdSnapshot) []string {
	result := []string{}
	for _, part := range s.parts {
		for _, id := range part.contents.Unavailable {
			if s.version >= 4 {
				id = part.ID + "/" + id
			}
			result = append(result, id)
		}
	}
	slices.Sort(result)
	return result
}
func (b *householdBackups) writeArchive(ctx context.Context, dir, path string, s householdSnapshot) (string, int, error) {
	f, err := os.OpenFile(filepath.Join(dir, "backup.zip"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	z := zip.NewWriter(backup.StreamWriter(f))
	defer z.Close()
	m := backupManifest{CoreOnly: s.coreOnly, Format: "homeatlas-household", Version: s.version, HouseholdID: s.household.ID, CreatedAt: time.Now().UTC().Format(time.RFC3339), SHA256: map[string]string{}, UnavailableOriginals: b.missing(s)}
	if s.version >= 4 {
		m.Modules = s.parts
	}
	total := int64(0)
	entries := 0
	add := func(name string, src *os.File, limit int64, expected string, size int64) error {
		defer src.Close()
		defer backup.DiscardCache(src)
		info, err := src.Stat()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > limit {
			return errInvalidBackup
		}
		entry, err := z.Create(name)
		if err != nil {
			return err
		}
		digest, n, err := hashCopy(entry, src, limit)
		if err != nil {
			return err
		}
		total += n
		entries++
		if total > maxBackupBytes-(2<<20) || entries > maxBackupFiles+1 {
			return errInvalidBackup
		}
		if expected != "" && (digest != expected || n != size) {
			return errBackupOriginal
		}
		m.SHA256[name] = digest
		return ctx.Err()
	}
	src, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	if err = add("household.sqlite", src, maxSnapshotBytes, "", 0); err != nil {
		return "", 0, err
	}
	for _, part := range s.parts {
		p, _ := b.snapshotParticipant(part)
		for _, file := range part.contents.Files {
			src, cleanup, err := backup.OpenOriginal(ctx, p.Provider, file.Key)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					err = errBackupOriginal
				}
				return "", 0, err
			}
			err = add(b.partPrefix(s.version, p)+"/"+file.Key, src, p.MaxFileBytes(), file.SHA256, file.Size)
			cleanup()
			if err != nil {
				return "", 0, err
			}
		}
	}
	entry, err := z.Create("manifest.json")
	if err != nil {
		return "", 0, err
	}
	if err = json.NewEncoder(entry).Encode(m); err != nil {
		return "", 0, err
	}
	if err = z.Close(); err != nil {
		return "", 0, err
	}
	if err = f.Sync(); err != nil {
		return "", 0, err
	}
	backup.DiscardCache(f)
	return f.Name(), s.unavailableCount(), nil
}
func readManifest(f *zip.File) (backupManifest, error) {
	var m backupManifest
	if f.UncompressedSize64 > 2<<20 {
		return m, errInvalidBackup
	}
	src, err := f.Open()
	if err != nil {
		return m, err
	}
	defer src.Close()
	dec := json.NewDecoder(io.LimitReader(src, 2<<20))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&m); err != nil {
		return m, errInvalidBackup
	}
	if dec.Decode(new(any)) != io.EOF || m.Format != "homeatlas-household" || m.Version < 1 || m.Version > 4 || len(m.SHA256) > maxBackupFiles+1 || len(m.UnavailableOriginals) > maxBackupFiles || len(m.Modules) > 1000 {
		return m, errInvalidBackup
	}
	if _, err = time.Parse(time.RFC3339, m.CreatedAt); err != nil || !backupKey.MatchString(m.HouseholdID) {
		return m, errInvalidBackup
	}
	if m.Version < 4 && len(m.Modules) > 0 {
		return m, errInvalidBackup
	}
	return m, nil
}
func (b *householdBackups) archiveName(name string) bool {
	if name == "manifest.json" || name == "household.sqlite" {
		return true
	}
	parts := strings.Split(name, "/")
	if len(parts) == 3 {
		return parts[0] == "modules" && moduleID.MatchString(parts[1]) && backupKey.MatchString(parts[2])
	}
	if len(parts) == 2 {
		p, ok := b.legacy()
		return ok && parts[0] == p.LegacyPrefix && backupKey.MatchString(parts[1])
	}
	return false
}
func (b *householdBackups) extractBackup(ctx context.Context, path, dir, household string) (householdSnapshot, error) {
	var empty householdSnapshot
	archive, err := os.Open(path)
	if err != nil {
		return empty, errInvalidBackup
	}
	defer archive.Close()
	defer backup.DiscardCache(archive)
	info, err := archive.Stat()
	if err != nil {
		return empty, errInvalidBackup
	}
	z, err := zip.NewReader(archive, info.Size())
	if err != nil {
		return empty, errInvalidBackup
	}
	entries := map[string]*zip.File{}
	var total uint64
	for _, f := range z.File {
		if len(entries) >= maxBackupFiles+2 || entries[f.Name] != nil || !f.Mode().IsRegular() || !b.archiveName(f.Name) {
			return empty, errInvalidBackup
		}
		if f.UncompressedSize64 > uint64(maxBackupBytes) {
			return empty, errInvalidBackup
		}
		total += f.UncompressedSize64
		if total > uint64(maxBackupBytes) {
			return empty, errInvalidBackup
		}
		entries[f.Name] = f
	}
	manifest, ok := entries["manifest.json"]
	if !ok {
		return empty, errInvalidBackup
	}
	m, err := readManifest(manifest)
	if err != nil {
		return empty, err
	}
	if household != "" && m.HouseholdID != household {
		return empty, errBackupConflict
	}
	if len(m.SHA256) != len(entries)-1 || entries["household.sqlite"] == nil {
		return empty, errInvalidBackup
	}
	header := householdSnapshot{version: m.Version, coreOnly: m.CoreOnly, parts: m.Modules}
	if m.Version < 4 && !m.CoreOnly {
		p, ok := b.legacy()
		if !ok {
			return empty, errBackupModuleRequired
		}
		header.parts = []snapshotPart{{ID: p.ID(), Version: p.Version()}}
	}
	if m.Version >= 4 && m.CoreOnly != (len(m.Modules) == 0) {
		return empty, errInvalidBackup
	}
	if err := validatePartIDs(header.parts); err != nil {
		return empty, err
	}
	schemas, err := b.schemas(header)
	if err != nil {
		return empty, err
	}
	limits := map[string]int64{}
	for _, part := range header.parts {
		p, _ := b.snapshotParticipant(part)
		limits[b.partPrefix(header.version, p)+"/"] = p.MaxFileBytes()
	}
	for name, f := range entries {
		if name == "manifest.json" {
			continue
		}
		limit := int64(0)
		if name == "household.sqlite" {
			limit = maxSnapshotBytes
		} else {
			for prefix, bound := range limits {
				if strings.HasPrefix(name, prefix) {
					limit = bound
					break
				}
			}
		}
		if limit == 0 {
			return empty, errInvalidBackup
		}
		if f.UncompressedSize64 > uint64(limit) {
			return empty, errInvalidBackup
		}
		if err = ctx.Err(); err != nil {
			return empty, err
		}
		src, err := f.Open()
		if err != nil {
			return empty, errInvalidBackup
		}
		target := filepath.Join(dir, name)
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			src.Close()
			return empty, err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			src.Close()
			return empty, err
		}
		digest, _, copyErr := hashCopy(out, src, limit)
		src.Close()
		syncErr := out.Sync()
		backup.DiscardCache(archive)
		backup.DiscardCache(out)
		closeErr := out.Close()
		if copyErr != nil || syncErr != nil || closeErr != nil || m.SHA256[name] != digest {
			return empty, errInvalidBackup
		}
	}
	path = filepath.Join(dir, "household.sqlite")
	validatedFile, err := os.Open(path)
	if err != nil {
		return empty, errInvalidBackup
	}
	defer validatedFile.Close()
	// Validation can now scan substantial plugin data. Drop its clean cache
	// before the safety backup allocates scrypt's workspace.
	defer backup.DiscardCache(validatedFile)
	db, err := openSnapshot(path)
	if err != nil {
		return empty, errInvalidBackup
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, "SELECT sql FROM sqlite_master WHERE sql IS NOT NULL ORDER BY name")
	if err != nil {
		return empty, errInvalidBackup
	}
	schema := []string{}
	for rows.Next() {
		var ddl string
		if rows.Scan(&ddl) != nil {
			rows.Close()
			return empty, errInvalidBackup
		}
		schema = append(schema, ddl)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(schema) != len(schemas) {
		return empty, errInvalidBackup
	}
	for _, ddl := range schema {
		if !slices.Contains(schemas, ddl) {
			return empty, errInvalidBackup
		}
	}
	var integrity string
	if err = db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		return empty, errInvalidBackup
	}
	var households int
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM household").Scan(&households); err != nil || households != 1 {
		return empty, errInvalidBackup
	}
	s, err := readCore(ctx, db, false)
	if err != nil {
		return empty, errInvalidBackup
	}
	s.version = m.Version
	s.coreOnly = m.CoreOnly
	s.parts = header.parts
	s.path = path
	s.members, err = readMembers(ctx, db, s.version)
	if err != nil {
		return empty, errInvalidBackup
	}
	for i, part := range s.parts {
		p, _ := b.snapshotParticipant(part)
		s.parts[i].contents, err = p.Inspect(ctx, db, s.household.ID)
		if err != nil {
			if errors.Is(err, backup.ModuleRequired) {
				return empty, errBackupModuleRequired
			}
			return empty, errInvalidBackup
		}
	}
	if s.version < 4 && s.coreOnly {
		p, _ := b.legacy()
		c, err := p.Inspect(ctx, db, s.household.ID)
		if err != nil || c.Records != 0 || len(c.Files) != 0 || len(c.Unavailable) != 0 {
			return empty, errInvalidBackup
		}
	}
	if err = validateCore(s, m.HouseholdID); err != nil {
		return empty, err
	}
	expectedMissing := b.missing(s)
	actualMissing := append([]string{}, m.UnavailableOriginals...)
	slices.Sort(actualMissing)
	if !slices.Equal(expectedMissing, actualMissing) {
		return empty, errInvalidBackup
	}
	expected := map[string]bool{"manifest.json": true, "household.sqlite": true}
	for _, part := range s.parts {
		p, _ := b.snapshotParticipant(part)
		for _, file := range part.contents.Files {
			name := b.partPrefix(s.version, p) + "/" + file.Key
			if expected[name] || m.SHA256[name] != file.SHA256 {
				return empty, errInvalidBackup
			}
			expected[name] = true
			if f := entries[name]; f == nil || int64(f.UncompressedSize64) != file.Size {
				return empty, errInvalidBackup
			}
		}
	}
	if len(expected) != len(entries) {
		return empty, errInvalidBackup
	}
	return s, nil
}
func validatePartIDs(parts []snapshotPart) error {
	seen := map[string]bool{}
	for _, part := range parts {
		if !moduleID.MatchString(part.ID) || part.Version < 1 || seen[part.ID] {
			return errInvalidBackup
		}
		seen[part.ID] = true
	}
	return nil
}
