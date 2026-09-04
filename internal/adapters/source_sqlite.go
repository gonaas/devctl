package adapters

import (
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"sync"

	_ "modernc.org/sqlite"

	"github.com/gonaas/devctl/internal/registry"
)

// sqliteSource is a generic, query-driven, strictly read-only project source.
//
// The database path, the query and the required columns all come from registry
// data, so this type knows nothing about which product wrote the file.
type sqliteSource struct {
	definition registry.ProjectSource
	once       sync.Once
	cached     Availability
}

func newSQLiteSource(definition registry.ProjectSource) ProjectSource {
	return &sqliteSource{definition: definition}
}

func (s *sqliteSource) Name() string { return s.definition.Name }

// open connects read-only. The file itself is never modified: mode=ro plus
// query_only means no statement can write and no checkpoint can run, so the
// database stays byte-identical.
//
// SQLite will still create the empty -shm and -wal side files it needs to read a
// write-ahead-log database consistently while another process writes. That is
// required for correctness, not a side effect worth avoiding: immutable=1
// suppresses those files but asserts the database cannot change, which risks
// reading torn data from a live writer.
func (s *sqliteSource) open() (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=query_only(1)", s.definition.Database)
	return sql.Open("sqlite", dsn)
}

func (s *sqliteSource) Available() Availability {
	s.once.Do(func() {
		switch {
		case s.definition.Database == "":
			s.cached = Availability{Reason: "no database declared"}
			return
		case !fileExists(s.definition.Database):
			s.cached = Availability{Reason: "database not found: " + s.definition.Database}
			return
		case s.definition.Query == "":
			s.cached = Availability{Reason: "no query declared"}
			return
		}

		database, err := s.open()
		if err != nil {
			s.cached = Availability{Reason: "cannot open database: " + err.Error()}
			return
		}
		defer database.Close()

		rows, err := database.Query(s.definition.Query)
		if err != nil {
			s.cached = Availability{Reason: "query failed: " + err.Error()}
			return
		}
		defer rows.Close()

		columns, err := rows.Columns()
		if err != nil {
			s.cached = Availability{Reason: "cannot read columns: " + err.Error()}
			return
		}
		present := map[string]bool{}
		for _, column := range columns {
			present[column] = true
		}
		for _, required := range s.definition.RequiredColumns {
			if !present[required] {
				s.cached = Availability{Reason: "missing column: " + required}
				return
			}
		}
		s.cached = Availability{Usable: true}
	})
	return s.cached
}

func (s *sqliteSource) Projects() []ProjectRecord {
	if !s.Available().Usable {
		return nil
	}
	database, err := s.open()
	if err != nil {
		return nil
	}
	defer database.Close()

	rows, err := database.Query(s.definition.Query)
	if err != nil {
		return nil
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil
	}

	var records []ProjectRecord
	for rows.Next() {
		values := make([]any, len(columns))
		holders := make([]any, len(columns))
		for index := range values {
			holders[index] = &values[index]
		}
		if err := rows.Scan(holders...); err != nil {
			continue
		}
		fields := map[string]string{}
		for index, column := range columns {
			fields[column] = asText(values[index])
		}
		if fields["project"] == "" || fields["directory"] == "" {
			continue
		}
		weight, _ := strconv.Atoi(fields["weight"])
		records = append(records, ProjectRecord{
			Project:    fields["project"],
			Directory:  fields["directory"],
			Weight:     weight,
			LastActive: fields["last_active"],
			Source:     s.definition.Name,
		})
	}
	return records
}

func asText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return fmt.Sprint(typed)
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
