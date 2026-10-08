package journal

import (
	"database/sql"
	"errors"
	"fmt"
)

var ErrOperationConflict = errors.New("operation ID reused with different payload")

// ReserveOperation is committed before native dispatch. A missing response on
// retry is deliberately uncertain, including after process restart.
func (s *Store) ReserveOperation(scope, id, digest string) (bool, []byte, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, nil, err
	}
	defer tx.Rollback()
	var existing string
	var response []byte
	err = tx.QueryRow(`SELECT digest,response FROM operations WHERE scope=? AND id=?`, scope, id).Scan(&existing, &response)
	if err == nil {
		if existing != digest {
			return false, nil, ErrOperationConflict
		}
		return false, response, nil
	}
	if err != sql.ErrNoRows {
		return false, nil, err
	}
	if _, err = tx.Exec(`INSERT INTO operations(scope,id,digest) VALUES(?,?,?)`, scope, id, digest); err != nil {
		return false, nil, err
	}
	if err = tx.Commit(); err != nil {
		return false, nil, err
	}
	return true, nil, nil
}

func (s *Store) ReadOperation(scope, id string) (bool, []byte, error) {
	var response []byte
	err := s.db.QueryRow(`SELECT response FROM operations WHERE scope=? AND id=?`, scope, id).Scan(&response)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil, nil
	}
	return err == nil, response, err
}

func (s *Store) PendingOperations() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM operations WHERE response IS NULL`).Scan(&count)
	return count, err
}

func (s *Store) CompleteOperation(scope, id string, response []byte) error {
	result, err := s.db.Exec(`UPDATE operations SET response=? WHERE scope=? AND id=? AND response IS NULL`, response, scope, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("operation completion lost reservation")
	}
	return nil
}
