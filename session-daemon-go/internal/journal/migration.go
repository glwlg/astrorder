package journal

import "database/sql"

func migrateTimestamp(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`PRAGMA table_info(frames)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, kind string
		var def any
		if err = rows.Scan(&cid, &name, &kind, &notnull, &def, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "timestamp" {
			found = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if !found {
		if _, err = tx.Exec(`ALTER TABLE frames ADD COLUMN timestamp REAL NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	return tx.Commit()
}
