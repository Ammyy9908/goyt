package goyt

import (
	"os"
	"path/filepath"
)

// JobLock represents an OS-backed exclusive lock on a job directory.
// It is automatically released when Close is called or when the holding
// process exits or terminates abnormally.
type JobLock struct {
	raw *fileLock
}

// AcquireJobLock attempts to acquire an exclusive, non-blocking lock on the job directory.
// If another process already holds the lock, ErrJobLocked is returned.
func AcquireJobLock(jobDir string) (*JobLock, error) {
	if err := os.MkdirAll(jobDir, 0700); err != nil {
		return nil, err
	}

	lockPath := filepath.Join(jobDir, "job.lock")
	fl, err := acquireLock(lockPath)
	if err != nil {
		return nil, err
	}

	return &JobLock{raw: fl}, nil
}

// Close releases the lock and closes the underlying file handle.
func (l *JobLock) Close() error {
	if l == nil || l.raw == nil {
		return nil
	}
	err := l.raw.Close()
	l.raw = nil
	return err
}
