package engine

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

type store struct {
	db     *sql.DB
	cipher cipher.AEAD
	lock   *os.File
}

func openStore(dir string, key []byte) (*store, error) {
	if len(key) != 32 {
		return nil, errors.New("加密密钥必须为 32 字节")
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	lock, e := os.OpenFile(filepath.Join(dir, "engine.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
		lock.Close()
		return nil, errors.New("下载核心已在运行")
	}
	b, _ := aes.NewCipher(key)
	a, _ := cipher.NewGCM(b)
	db, e := sql.Open("sqlite", filepath.Join(dir, "tasks.sqlite"))
	if e != nil {
		lock.Close()
		return nil, e
	}
	db.SetMaxOpenConns(1)
	s := &store{db, a, lock}
	_, e = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA busy_timeout=5000;
CREATE TABLE IF NOT EXISTS records (id TEXT PRIMARY KEY, payload BLOB NOT NULL); PRAGMA user_version=1;`)
	if e != nil {
		s.close()
		return nil, e
	}
	if e = os.Chmod(filepath.Join(dir, "tasks.sqlite"), 0600); e != nil {
		s.close()
		return nil, e
	}
	return s, nil
}

func (s *store) put(id string, v any) error {
	p, e := json.Marshal(v)
	if e != nil {
		return e
	}
	n := make([]byte, s.cipher.NonceSize())
	if _, e = rand.Read(n); e != nil {
		return e
	}
	sealed := s.cipher.Seal(n, n, p, []byte(id))
	_, e = s.db.Exec("INSERT INTO records(id,payload) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload", id, sealed)
	return e
}

func (s *store) all() (map[string]json.RawMessage, error) {
	rows, e := s.db.Query("SELECT id,payload FROM records")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := map[string]json.RawMessage{}
	for rows.Next() {
		var id string
		var p []byte
		if e = rows.Scan(&id, &p); e != nil {
			return nil, e
		}
		n := s.cipher.NonceSize()
		if len(p) < n {
			return nil, errors.New("任务数据库已损坏")
		}
		plain, e := s.cipher.Open(nil, p[:n], p[n:], []byte(id))
		if e != nil {
			return nil, errors.New("无法解密任务数据库，请检查钥匙串密钥")
		}
		out[id] = plain
	}
	return out, rows.Err()
}

func (s *store) remove(id string) error {
	_, e := s.db.Exec("DELETE FROM records WHERE id=?", id)
	return e
}
func (s *store) close() { s.db.Close(); unix.Flock(int(s.lock.Fd()), unix.LOCK_UN); s.lock.Close() }
