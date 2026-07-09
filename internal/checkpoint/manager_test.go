package checkpoint

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewManager(t *testing.T) {
	m := NewManager("/tmp/checkpoint-test")
	assert.NotNil(t, m)
	assert.Equal(t, "/tmp/checkpoint-test", m.dataDir)
}

func TestCreateCheckpoint(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	plan := CheckpointPlan{
		RecoveryID:   "rec-001",
		TableName:    "orders",
		RecoveryTime: now,
		TotalBatches: 5,
		TableOrder:   []string{"users", "orders"},
	}

	cp, err := m.CreateCheckpoint(context.Background(), plan)
	require.NoError(t, err)
	assert.Equal(t, "rec-001", cp.RecoveryID)
	assert.Equal(t, "orders", cp.TableName)
	assert.Equal(t, now, cp.RecoveryTime)
	assert.Equal(t, 5, cp.TotalBatches)
	assert.Equal(t, 0, cp.CompletedBatches)
	assert.Equal(t, "in_progress", cp.Status)
	assert.Empty(t, cp.Checksum)
	assert.Equal(t, []string{"users", "orders"}, cp.TableOrder)
	assert.False(t, cp.LastCommitAt.IsZero())

	// Verify file was written.
	checkFile := filepath.Join(dir, "checkpoints", "rec-001.json")
	_, err = os.Stat(checkFile)
	assert.NoError(t, err, "checkpoint file should exist")
}

func TestCreateCheckpoint_MkdirAll(t *testing.T) {
	// Manager should create the checkpoints directory automatically.
	dir := filepath.Join(t.TempDir(), "nested", "dir")
	m := NewManager(dir)

	plan := CheckpointPlan{
		RecoveryID:   "rec-mkdir",
		TableName:    "t1",
		RecoveryTime: time.Now(),
		TotalBatches: 1,
	}

	cp, err := m.CreateCheckpoint(context.Background(), plan)
	require.NoError(t, err)
	assert.Equal(t, "rec-mkdir", cp.RecoveryID)

	checkDir := filepath.Join(dir, "checkpoints")
	info, err := os.Stat(checkDir)
	assert.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestUpdateBatch(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	plan := CheckpointPlan{
		RecoveryID:   "rec-002",
		TableName:    "orders",
		RecoveryTime: time.Now(),
		TotalBatches: 10,
	}
	_, err := m.CreateCheckpoint(context.Background(), plan)
	require.NoError(t, err)

	err = m.UpdateBatch(context.Background(), "rec-002", 3)
	require.NoError(t, err)

	// Re-read to verify.
	cp, err := m.GetLastCheckpoint(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 3, cp.CompletedBatches)
	assert.Equal(t, "in_progress", cp.Status)
}

func TestUpdateBatch_NotFound(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	err := m.UpdateBatch(context.Background(), "nonexistent", 1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent")
}

func TestComplete(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	plan := CheckpointPlan{
		RecoveryID:   "rec-003",
		TableName:    "orders",
		RecoveryTime: time.Now(),
		TotalBatches: 5,
	}
	_, err := m.CreateCheckpoint(context.Background(), plan)
	require.NoError(t, err)

	err = m.UpdateBatch(context.Background(), "rec-003", 5)
	require.NoError(t, err)

	err = m.Complete(context.Background(), "rec-003")
	require.NoError(t, err)

	cp, err := m.GetLastCheckpoint(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "complete", cp.Status)
	assert.NotEmpty(t, cp.Checksum)
	assert.Len(t, cp.Checksum, 64) // SHA256 hex
}

func TestComplete_NotFound(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	err := m.Complete(context.Background(), "nonexistent")
	assert.Error(t, err)
}

func TestGetLastCheckpoint_Empty(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	cp, err := m.GetLastCheckpoint(context.Background())
	require.NoError(t, err)
	assert.Nil(t, cp)
}

func TestGetLastCheckpoint_Single(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	plan := CheckpointPlan{
		RecoveryID:   "rec-latest",
		TableName:    "t1",
		RecoveryTime: time.Now(),
		TotalBatches: 1,
	}
	cp1, err := m.CreateCheckpoint(context.Background(), plan)
	require.NoError(t, err)

	cp, err := m.GetLastCheckpoint(context.Background())
	require.NoError(t, err)
	assert.Equal(t, cp1.RecoveryID, cp.RecoveryID)
}

func TestGetLastCheckpoint_Multiple(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	_, err := m.CreateCheckpoint(context.Background(), CheckpointPlan{
		RecoveryID: "rec-old", TableName: "t1", RecoveryTime: time.Now(), TotalBatches: 1,
	})
	require.NoError(t, err)

	time.Sleep(10 * time.Millisecond) // ensure distinct timestamps

	_, err = m.CreateCheckpoint(context.Background(), CheckpointPlan{
		RecoveryID: "rec-new", TableName: "t2", RecoveryTime: time.Now(), TotalBatches: 1,
	})
	require.NoError(t, err)

	cp, err := m.GetLastCheckpoint(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "rec-new", cp.RecoveryID)
}

func TestListCheckpoints(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	// Empty list.
	checkpoints, err := m.ListCheckpoints(context.Background())
	require.NoError(t, err)
	assert.Empty(t, checkpoints)

	// Create two checkpoints.
	_, err = m.CreateCheckpoint(context.Background(), CheckpointPlan{
		RecoveryID: "rec-aaa", TableName: "t1", RecoveryTime: time.Now(), TotalBatches: 2,
	})
	require.NoError(t, err)

	time.Sleep(10 * time.Millisecond)

	_, err = m.CreateCheckpoint(context.Background(), CheckpointPlan{
		RecoveryID: "rec-bbb", TableName: "t2", RecoveryTime: time.Now(), TotalBatches: 3,
	})
	require.NoError(t, err)

	checkpoints, err = m.ListCheckpoints(context.Background())
	require.NoError(t, err)
	assert.Len(t, checkpoints, 2)

	// Should be sorted newest-first.
	assert.Equal(t, "rec-bbb", checkpoints[0].RecoveryID)
	assert.Equal(t, "rec-aaa", checkpoints[1].RecoveryID)
}

func TestAtomicWrite_CleanupOnRenameFailure(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	// Create a checkpoint normally.
	plan := CheckpointPlan{
		RecoveryID: "rec-atomic", TableName: "t1", RecoveryTime: time.Now(), TotalBatches: 1,
	}
	cp, err := m.CreateCheckpoint(context.Background(), plan)
	require.NoError(t, err)
	assert.NotNil(t, cp)

	// Verify the .tmp file was cleaned up.
	tmpPath := filepath.Join(dir, "checkpoints", "rec-atomic.json.tmp")
	_, err = os.Stat(tmpPath)
	assert.True(t, os.IsNotExist(err), "temp file should be removed after successful write")
}

func TestCorruptFile_Skipped(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	// Write a corrupt JSON file.
	checkDir := filepath.Join(dir, "checkpoints")
	require.NoError(t, os.MkdirAll(checkDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(checkDir, "corrupt.json"), []byte("{bad json"), 0644))

	// Create a valid checkpoint.
	_, err := m.CreateCheckpoint(context.Background(), CheckpointPlan{
		RecoveryID: "rec-good", TableName: "t1", RecoveryTime: time.Now(), TotalBatches: 1,
	})
	require.NoError(t, err)

	// List should skip corrupt and return only valid.
	checkpoints, err := m.ListCheckpoints(context.Background())
	require.NoError(t, err)
	assert.Len(t, checkpoints, 1)
	assert.Equal(t, "rec-good", checkpoints[0].RecoveryID)
}

func TestConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	_, err := m.CreateCheckpoint(context.Background(), CheckpointPlan{
		RecoveryID: "rec-con", TableName: "t1", RecoveryTime: time.Now(), TotalBatches: 100,
	})
	require.NoError(t, err)

	// Simulate concurrent updates from multiple goroutines.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 20; i++ {
			_ = m.UpdateBatch(context.Background(), "rec-con", i)
		}
		done <- struct{}{}
	}()
	go func() {
		for i := 0; i < 20; i++ {
			_, _ = m.GetLastCheckpoint(context.Background())
		}
		done <- struct{}{}
	}()
	go func() {
		for i := 0; i < 20; i++ {
			_, _ = m.ListCheckpoints(context.Background())
		}
		done <- struct{}{}
	}()

	// Wait for all goroutines.
	for i := 0; i < 3; i++ {
		<-done
	}

	// Final commit.
	err = m.Complete(context.Background(), "rec-con")
	require.NoError(t, err)

	cp, err := m.GetLastCheckpoint(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "complete", cp.Status)
	assert.NotEmpty(t, cp.Checksum)
}

// ---------------------------------------------------------------------------
// Crash recovery — resume from checkpoint
// ---------------------------------------------------------------------------

func TestResumeFromCheckpoint(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	// Create a checkpoint simulating partial progress (5 of 20 batches done).
	plan := CheckpointPlan{
		RecoveryID:   "rec-crash",
		TableName:    "orders",
		RecoveryTime: time.Now(),
		TotalBatches: 20,
		TableOrder:   []string{"users", "orders"},
	}
	cp, err := m.CreateCheckpoint(context.Background(), plan)
	require.NoError(t, err)
	assert.Equal(t, "in_progress", cp.Status)
	assert.Equal(t, 0, cp.CompletedBatches)

	// Simulate completing 5 batches before crash.
	err = m.UpdateBatch(context.Background(), "rec-crash", 5)
	require.NoError(t, err)

	// Verify partial state.
	cp, err = m.GetLastCheckpoint(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 5, cp.CompletedBatches)

	// Simulate crash by creating a new Manager pointing at the same directory.
	m2 := NewManager(dir)

	cp2, err := m2.GetLastCheckpoint(context.Background())
	require.NoError(t, err)
	require.NotNil(t, cp2)

	// Verify the resumed state matches.
	assert.Equal(t, "rec-crash", cp2.RecoveryID)
	assert.Equal(t, "orders", cp2.TableName)
	assert.Equal(t, 20, cp2.TotalBatches)
	assert.Equal(t, 5, cp2.CompletedBatches)
	assert.Equal(t, "in_progress", cp2.Status)
	assert.Equal(t, []string{"users", "orders"}, cp2.TableOrder)

	// Verify we can continue from where we left off.
	err = m2.UpdateBatch(context.Background(), "rec-crash", 10)
	require.NoError(t, err)

	err = m2.Complete(context.Background(), "rec-crash")
	require.NoError(t, err)

	cp3, err := m2.GetLastCheckpoint(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "complete", cp3.Status)
	assert.NotEmpty(t, cp3.Checksum)
	assert.Len(t, cp3.Checksum, 64) // SHA256 hex
	assert.Equal(t, 10, cp3.CompletedBatches)
}
