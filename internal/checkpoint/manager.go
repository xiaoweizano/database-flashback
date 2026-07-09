package checkpoint

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Manager persists rollback-execution checkpoint state to the filesystem.
// It uses atomic writes (temp file + rename) and is thread-safe via RWMutex.
type Manager struct {
	dataDir string
	mu      sync.RWMutex
}

// NewManager creates a new checkpoint manager rooted at dataDir. The
// checkpoints directory is created lazily on the first write.
func NewManager(dataDir string) *Manager {
	return &Manager{dataDir: dataDir}
}

// checkpointDir returns the path to the checkpoints sub-directory.
func (m *Manager) checkpointDir() string {
	return filepath.Join(m.dataDir, "checkpoints")
}

// checkpointPath returns the file path for a given recovery ID.
func (m *Manager) checkpointPath(id string) string {
	return filepath.Join(m.checkpointDir(), id+".json")
}

// CreateCheckpoint persists a new in-progress checkpoint for the given plan.
func (m *Manager) CreateCheckpoint(ctx context.Context, plan CheckpointPlan) (*Checkpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := os.MkdirAll(m.checkpointDir(), 0755); err != nil {
		return nil, fmt.Errorf("checkpoint: create dir: %w", err)
	}

	cp := &Checkpoint{
		RecoveryID:       plan.RecoveryID,
		TableName:        plan.TableName,
		RecoveryTime:     plan.RecoveryTime,
		TotalBatches:     plan.TotalBatches,
		CompletedBatches: 0,
		TableOrder:       plan.TableOrder,
		LastCommitAt:     time.Now(),
		Status:           "in_progress",
	}

	if err := m.atomicWrite(cp); err != nil {
		return nil, err
	}
	return cp, nil
}

// UpdateBatch advances the completed-batch counter for the checkpoint with
// the given recovery ID.
func (m *Manager) UpdateBatch(ctx context.Context, id string, completed int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cp, err := m.readCheckpoint(id)
	if err != nil {
		return err
	}

	cp.CompletedBatches = completed
	cp.LastCommitAt = time.Now()

	return m.atomicWrite(cp)
}

// Complete marks the checkpoint as complete and computes its SHA256 checksum.
func (m *Manager) Complete(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cp, err := m.readCheckpoint(id)
	if err != nil {
		return err
	}

	cp.Status = "complete"
	cp.LastCommitAt = time.Now()

	// Serialise to compute a stable checksum.
	data, err := json.Marshal(cp)
	if err != nil {
		return fmt.Errorf("checkpoint: marshal for checksum: %w", err)
	}
	h := sha256.Sum256(data)
	cp.Checksum = fmt.Sprintf("%x", h)

	return m.atomicWrite(cp)
}

// GetLastCheckpoint returns the most recent checkpoint by LastCommitAt, or nil
// if no checkpoints exist.
func (m *Manager) GetLastCheckpoint(ctx context.Context) (*Checkpoint, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	checkpoints, err := m.listCheckpoints()
	if err != nil {
		return nil, err
	}
	if len(checkpoints) == 0 {
		return nil, nil
	}
	return &checkpoints[0], nil // already sorted newest-first
}

// ListCheckpoints returns all known checkpoints sorted by LastCommitAt
// descending.
func (m *Manager) ListCheckpoints(ctx context.Context) ([]Checkpoint, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.listCheckpoints()
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// listCheckpoints scans the checkpoints directory and returns all valid
// checkpoint files sorted by LastCommitAt descending. Must be called with at
// least a read lock held.
func (m *Manager) listCheckpoints() ([]Checkpoint, error) {
	dir := m.checkpointDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Checkpoint{}, nil
		}
		return nil, fmt.Errorf("checkpoint: read dir: %w", err)
	}

	var checkpoints []Checkpoint
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		cp, err := m.readCheckpoint(id)
		if err != nil {
			continue // skip corrupt files
		}
		checkpoints = append(checkpoints, *cp)
	}

	sort.Slice(checkpoints, func(i, j int) bool {
		return checkpoints[i].LastCommitAt.After(checkpoints[j].LastCommitAt)
	})
	return checkpoints, nil
}

// readCheckpoint deserializes a single checkpoint file. Must be called with
// at least a read lock held.
func (m *Manager) readCheckpoint(id string) (*Checkpoint, error) {
	path := m.checkpointPath(id)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("checkpoint: read %s: %w", id, err)
	}

	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("checkpoint: unmarshal %s: %w", id, err)
	}
	return &cp, nil
}

// atomicWrite serialises cp to a temporary file and atomically renames it
// into place. Must be called with the write lock held.
func (m *Manager) atomicWrite(cp *Checkpoint) error {
	path := m.checkpointPath(cp.RecoveryID)
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return fmt.Errorf("checkpoint: marshal: %w", err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("checkpoint: write tmp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath) // best-effort cleanup
		return fmt.Errorf("checkpoint: rename: %w", err)
	}
	return nil
}
