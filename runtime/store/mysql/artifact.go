package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/hexagon-codes/hexagon/agent/artifact"
)

// ArtifactStore 与运行权威使用同一宿主数据库，但字节仍由外部 ObjectStore 管理。
// scope 在装配时绑定，每次查询都带作用域；不关闭注入连接、不隐式迁移。
type ArtifactStore struct {
	db    *sql.DB
	scope string
}

func NewArtifactStore(db *sql.DB, scope string) (*ArtifactStore, error) {
	if db == nil || scope == "" {
		return nil, fmt.Errorf("mysql: artifact store requires database and scope")
	}
	return &ArtifactStore{db: db, scope: scope}, nil
}

func (s *ArtifactStore) Save(ctx context.Context, art artifact.Artifact) (string, error) {
	return s.save(ctx, art, false, false)
}
func (s *ArtifactStore) SaveNextVersion(ctx context.Context, art artifact.Artifact) (string, error) {
	return s.save(ctx, art, true, false)
}
func (s *ArtifactStore) CreateNextVersion(ctx context.Context, art artifact.Artifact) (string, error) {
	return s.save(ctx, art, true, true)
}

// 同一 (scope,session,name) 的版本分配与对象创建在数据库锁及事务内完成。
func (s *ArtifactStore) save(ctx context.Context, art artifact.Artifact, allocate, create bool) (string, error) {
	if art.SessionID == "" || art.Name == "" {
		return "", fmt.Errorf("mysql: artifact session and name are required")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO hexagon_artifact_heads(scope,session_id,name,version) VALUES(?,?,?,0) ON DUPLICATE KEY UPDATE version=version`, s.scope, art.SessionID, art.Name); err != nil {
		return "", err
	}
	var ignored int64
	if err := tx.QueryRowContext(ctx, `SELECT version FROM hexagon_artifact_heads WHERE scope=? AND session_id=? AND name=? FOR UPDATE`, s.scope, art.SessionID, art.Name).Scan(&ignored); err != nil {
		return "", err
	}
	if allocate {
		var maximum int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM hexagon_artifacts WHERE scope=? AND session_id=? AND name=?`, s.scope, art.SessionID, art.Name).Scan(&maximum); err != nil {
			return "", err
		}
		art.Version = maximum + 1
	}
	if art.ID == "" {
		art.ID = fmt.Sprintf("%s/%s/v%d", art.SessionID, art.Name, art.Version)
	}
	if art.CreatedAt.IsZero() {
		art.CreatedAt = time.Now().UTC()
	}
	data, err := json.Marshal(art)
	if err != nil {
		return "", err
	}
	statement := `INSERT INTO hexagon_artifacts(scope,artifact_id,session_id,name,version,record) VALUES(?,?,?,?,?,?)`
	if !create {
		statement += ` ON DUPLICATE KEY UPDATE session_id=VALUES(session_id),name=VALUES(name),version=VALUES(version),record=VALUES(record)`
	}
	if _, err := tx.ExecContext(ctx, statement, s.scope, art.ID, art.SessionID, art.Name, art.Version, data); err != nil {
		tx.Rollback()
		if create {
			if _, lookupErr := s.Get(ctx, art.ID); lookupErr == nil {
				return "", artifact.ErrArtifactConflict
			}
		}
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("mysql: artifact registration outcome is unknown: %w", err)
	}
	return art.ID, nil
}

func (s *ArtifactStore) Get(ctx context.Context, id string) (*artifact.Artifact, error) {
	return s.get(ctx, `SELECT record FROM hexagon_artifacts WHERE scope=? AND artifact_id=?`, s.scope, id)
}

func (s *ArtifactStore) get(ctx context.Context, statement string, args ...any) (*artifact.Artifact, error) {
	var data []byte
	if err := s.db.QueryRowContext(ctx, statement, args...).Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("mysql: artifact not found")
		}
		return nil, err
	}
	var art artifact.Artifact
	if err := json.Unmarshal(data, &art); err != nil {
		return nil, err
	}
	return &art, nil
}

func (s *ArtifactStore) GetByVersion(ctx context.Context, sessionID, name string, version int) (*artifact.Artifact, error) {
	return s.get(ctx, `SELECT record FROM hexagon_artifacts WHERE scope=? AND session_id=? AND name=? AND version=? ORDER BY artifact_id LIMIT 1`, s.scope, sessionID, name, version)
}

func (s *ArtifactStore) GetLatest(ctx context.Context, sessionID, name string) (*artifact.Artifact, error) {
	return s.get(ctx, `SELECT record FROM hexagon_artifacts WHERE scope=? AND session_id=? AND name=? ORDER BY version DESC,artifact_id LIMIT 1`, s.scope, sessionID, name)
}

func (s *ArtifactStore) list(ctx context.Context, sessionID, name string) ([]artifact.Artifact, error) {
	statement := `SELECT record FROM hexagon_artifacts WHERE scope=? AND session_id=?`
	args := []any{s.scope, sessionID}
	if name != "" {
		statement += ` AND name=?`
		args = append(args, name)
	}
	statement += ` ORDER BY name,version,artifact_id`
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []artifact.Artifact
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var art artifact.Artifact
		if err := json.Unmarshal(data, &art); err != nil {
			return nil, err
		}
		result = append(result, art)
	}
	return result, rows.Err()
}

func (s *ArtifactStore) ListVersions(ctx context.Context, sessionID, name string) ([]artifact.Artifact, error) {
	return s.list(ctx, sessionID, name)
}

func (s *ArtifactStore) List(ctx context.Context, sessionID string) ([]artifact.Artifact, error) {
	all, err := s.list(ctx, sessionID, "")
	if err != nil {
		return nil, err
	}
	latest := make(map[string]artifact.Artifact)
	for _, art := range all {
		if old, ok := latest[art.Name]; !ok || art.Version > old.Version {
			latest[art.Name] = art
		}
	}
	result := make([]artifact.Artifact, 0, len(latest))
	for _, art := range latest {
		result = append(result, art)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func (s *ArtifactStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM hexagon_artifacts WHERE scope=? AND artifact_id=?`, s.scope, id)
	return err
}
func (s *ArtifactStore) DeleteAll(ctx context.Context, sessionID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM hexagon_artifacts WHERE scope=? AND session_id=?`, s.scope, sessionID)
	return err
}

var _ artifact.Store = (*ArtifactStore)(nil)
