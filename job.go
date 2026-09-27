package goyt

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// JobStage identifies the current lifecycle phase of a persistent download job.
type JobStage string

const (
	JobStagePlanned       JobStage = "planned"
	JobStageDownloading   JobStage = "downloading"
	JobStageProcessing    JobStage = "processing"
	JobStageVerifying     JobStage = "verifying"
	JobStageReadyToCommit JobStage = "ready_to_commit"
	JobStageCompleted     JobStage = "completed"
)

// FormatIdentity captures the immutable identity of a media stream.
type FormatIdentity struct {
	ID              string   `json:"id"`
	Protocol        Protocol `json:"protocol"`
	Container       string   `json:"container"`
	VideoCodec      string   `json:"video_codec,omitempty"`
	AudioCodec      string   `json:"audio_codec,omitempty"`
	Width           *int     `json:"width,omitempty"`
	Height          *int     `json:"height,omitempty"`
	Bitrate         *int64   `json:"bitrate,omitempty"`
	SizeBytes       *int64   `json:"size_bytes,omitempty"`
	Language        string   `json:"language,omitempty"`
	AudioTrackID    string   `json:"audio_track_id,omitempty"`
	AudioTrackName  string   `json:"audio_track_name,omitempty"`
	AudioIsDefault  bool     `json:"audio_is_default,omitempty"`
	AudioIsOriginal bool     `json:"audio_is_original,omitempty"`
}

// JobStreamState tracks progress and integrity for an individual input stream.
type JobStreamState struct {
	Index        int            `json:"index"`
	Format       FormatIdentity `json:"format"`
	RelativePath string         `json:"relative_path"`
	Completed    bool           `json:"completed"`
	SizeBytes    int64          `json:"size_bytes,omitempty"`
	SHA256       string         `json:"sha256,omitempty"`
}

// JobSelection preserves the caller's selection constraints.
type JobSelection struct {
	MaxHeight     int    `json:"max_height,omitempty"`
	VideoCodec    string `json:"video_codec,omitempty"`
	AudioCodec    string `json:"audio_codec,omitempty"`
	Container     string `json:"container,omitempty"`
	AllowSeparate bool   `json:"allow_separate,omitempty"`
	AudioLanguage string `json:"audio_language,omitempty"`
	AudioFormat   string `json:"audio_format,omitempty"`
	AudioQuality  *int   `json:"audio_quality,omitempty"`
	AudioBitrate  string `json:"audio_bitrate,omitempty"`
}

// JobOutputSpec serializes audio output conversion parameters.
type JobOutputSpec struct {
	RequestedFormat string `json:"requested_format,omitempty"`
	ResolvedCodec   string `json:"resolved_codec,omitempty"`
	Container       string `json:"container,omitempty"`
	Extension       string `json:"extension,omitempty"`
	Copy            bool   `json:"copy,omitempty"`
	Encoder         string `json:"encoder,omitempty"`
	Quality         int    `json:"quality,omitempty"`
	Bitrate         string `json:"bitrate,omitempty"`
}

// JobPreCommit records final staged output checksum and size before destination replacement.
type JobPreCommit struct {
	StagedRelativePath string `json:"staged_relative_path,omitempty"`
	FinalSizeBytes     int64  `json:"final_size_bytes,omitempty"`
	FinalSHA256        string `json:"final_sha256,omitempty"`
}

// JobManifest represents the versioned on-disk state of a persistent download job.
type JobManifest struct {
	SchemaVersion   int              `json:"schema_version"`
	JobID           string           `json:"job_id"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
	SourceURL       string           `json:"source_url"`
	VideoID         string           `json:"video_id"`
	Title           string           `json:"title,omitempty"`
	DurationSeconds *float64         `json:"duration_seconds,omitempty"`
	DestinationPath string           `json:"destination_path"`
	Transport       string           `json:"transport"`
	Mode            string           `json:"mode"`
	Stage           JobStage         `json:"stage"`
	Selection       JobSelection     `json:"selection"`
	AudioOutputSpec *JobOutputSpec   `json:"audio_output_spec,omitempty"`
	NeedsMerge      bool             `json:"needs_merge,omitempty"`
	NeedsRemux      bool             `json:"needs_remux,omitempty"`
	OutputContainer string           `json:"output_container,omitempty"`
	DecodeCheck     bool             `json:"decode_check"`
	Streams         []JobStreamState `json:"streams"`
	PreCommit       *JobPreCommit    `json:"pre_commit,omitempty"`
	CompletedAt     *time.Time       `json:"completed_at,omitempty"`
}

func generateJobID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// FormatToIdentity extracts immutable identity fields from a Format.
func FormatToIdentity(f Format) FormatIdentity {
	return FormatIdentity{
		ID:              f.ID,
		Protocol:        f.Protocol,
		Container:       f.Container,
		VideoCodec:      f.VideoCodec,
		AudioCodec:      f.AudioCodec,
		Width:           cloneValue(f.Width),
		Height:          cloneValue(f.Height),
		Bitrate:         cloneValue(f.Bitrate),
		SizeBytes:       cloneValue(f.SizeBytes),
		Language:        f.Language,
		AudioTrackID:    f.AudioTrackID,
		AudioTrackName:  f.AudioTrackName,
		AudioIsDefault:  f.AudioIsDefault,
		AudioIsOriginal: f.AudioIsOriginal,
	}
}

// IdentityToFormat reconstructs a Format from FormatIdentity and an active Resource.
func IdentityToFormat(id FormatIdentity, res Resource) Format {
	return Format{
		ID:              id.ID,
		Protocol:        id.Protocol,
		Container:       id.Container,
		VideoCodec:      id.VideoCodec,
		AudioCodec:      id.AudioCodec,
		Width:           cloneValue(id.Width),
		Height:          cloneValue(id.Height),
		Bitrate:         cloneValue(id.Bitrate),
		SizeBytes:       cloneValue(id.SizeBytes),
		Language:        id.Language,
		AudioTrackID:    id.AudioTrackID,
		AudioTrackName:  id.AudioTrackName,
		AudioIsDefault:  id.AudioIsDefault,
		AudioIsOriginal: id.AudioIsOriginal,
		Resource:        res,
	}
}

// CreateVideoJob creates an initialized JobManifest for an HTTP video download.
func CreateVideoJob(
	sourceURL string,
	videoID string,
	title string,
	duration *time.Duration,
	destination string,
	plan *DownloadPlan,
	selection Selection,
	decodeCheck bool,
) (*JobManifest, error) {
	if plan == nil || len(plan.Streams) == 0 {
		return nil, errors.New("goyt: invalid video plan")
	}

	absDest, err := filepath.Abs(destination)
	if err != nil {
		return nil, err
	}

	var durSec *float64
	if duration != nil {
		s := duration.Seconds()
		durSec = &s
	}

	streams := make([]JobStreamState, len(plan.Streams))
	for i, s := range plan.Streams {
		streams[i] = JobStreamState{
			Index:        i,
			Format:       FormatToIdentity(s),
			RelativePath: filepath.Join("inputs", fmt.Sprintf("stream-%d.media", i)),
			Completed:    false,
		}
	}

	now := time.Now().UTC()
	return &JobManifest{
		SchemaVersion:   1,
		JobID:           generateJobID(),
		CreatedAt:       now,
		UpdatedAt:       now,
		SourceURL:       sourceURL,
		VideoID:         videoID,
		Title:           title,
		DurationSeconds: durSec,
		DestinationPath: absDest,
		Transport:       "http",
		Mode:            "video",
		Stage:           JobStagePlanned,
		Selection: JobSelection{
			MaxHeight:     selection.MaxHeight,
			VideoCodec:    selection.VideoCodec,
			AudioCodec:    selection.AudioCodec,
			Container:     selection.Container,
			AllowSeparate: selection.AllowSeparate,
		},
		NeedsMerge:      plan.NeedsMerge,
		NeedsRemux:      plan.NeedsRemux,
		OutputContainer: plan.OutputContainer,
		DecodeCheck:     decodeCheck,
		Streams:         streams,
	}, nil
}

// CreateAudioJob creates an initialized JobManifest for an HTTP audio-only download.
func CreateAudioJob(
	sourceURL string,
	videoID string,
	title string,
	duration *time.Duration,
	destination string,
	plan *AudioPlan,
	selection AudioSelection,
	decodeCheck bool,
) (*JobManifest, error) {
	if plan == nil {
		return nil, errors.New("goyt: invalid audio plan")
	}

	absDest, err := filepath.Abs(destination)
	if err != nil {
		return nil, err
	}

	var durSec *float64
	if duration != nil {
		s := duration.Seconds()
		durSec = &s
	}

	now := time.Now().UTC()
	return &JobManifest{
		SchemaVersion:   1,
		JobID:           generateJobID(),
		CreatedAt:       now,
		UpdatedAt:       now,
		SourceURL:       sourceURL,
		VideoID:         videoID,
		Title:           title,
		DurationSeconds: durSec,
		DestinationPath: absDest,
		Transport:       "http",
		Mode:            "audio-only",
		Stage:           JobStagePlanned,
		Selection: JobSelection{
			AudioLanguage: selection.AudioLanguage,
			AudioFormat:   selection.AudioFormat,
			AudioQuality:  selection.AudioQuality,
			AudioBitrate:  selection.AudioBitrate,
		},
		AudioOutputSpec: &JobOutputSpec{
			RequestedFormat: plan.OutputSpec.RequestedFormat,
			ResolvedCodec:   plan.OutputSpec.ResolvedCodec,
			Container:       plan.OutputSpec.Container,
			Extension:       plan.OutputSpec.Extension,
			Copy:            plan.OutputSpec.Copy,
			Encoder:         plan.OutputSpec.Encoder,
			Quality:         plan.OutputSpec.Quality,
			Bitrate:         plan.OutputSpec.Bitrate,
		},
		DecodeCheck: decodeCheck,
		Streams: []JobStreamState{
			{
				Index:        0,
				Format:       FormatToIdentity(plan.Stream),
				RelativePath: filepath.Join("inputs", "stream-0.media"),
				Completed:    false,
			},
		},
	}, nil
}

// Save writes the manifest atomically to jobDir/job.json.
func (m *JobManifest) Save(jobDir string) error {
	m.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	data = append(data, '\n')

	manifestPath := filepath.Join(jobDir, "job.json")
	tmpPath := filepath.Join(jobDir, fmt.Sprintf(".job.json.tmp-%d-%d", os.Getpid(), time.Now().UnixNano()))

	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}

	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(tmpPath)
		return err
	}

	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(tmpPath)
		return err
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	if err := os.Rename(tmpPath, manifestPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	return nil
}

// LoadJobManifest reads and validates jobDir/job.json.
func LoadJobManifest(jobDir string) (*JobManifest, error) {
	manifestPath := filepath.Join(jobDir, "job.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrJobNotFound
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}

	var m JobManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%w: JSON unmarshal error: %v", ErrInvalidManifest, err)
	}

	if err := m.Validate(jobDir); err != nil {
		return nil, err
	}

	return &m, nil
}

// Validate verifies that the manifest contains consistent, valid fields and safe paths.
func (m *JobManifest) Validate(jobDir string) error {
	if m.SchemaVersion != 1 {
		return fmt.Errorf("%w: version %d (supported: 1)", ErrUnsupportedManifestVersion, m.SchemaVersion)
	}
	if strings.TrimSpace(m.JobID) == "" {
		return fmt.Errorf("%w: missing job_id", ErrInvalidManifest)
	}
	if strings.TrimSpace(m.SourceURL) == "" || strings.TrimSpace(m.VideoID) == "" {
		return fmt.Errorf("%w: missing source_url or video_id", ErrInvalidManifest)
	}
	if strings.TrimSpace(m.DestinationPath) == "" || !filepath.IsAbs(m.DestinationPath) {
		return fmt.Errorf("%w: destination_path must be a non-empty absolute path", ErrInvalidManifest)
	}
	if m.Transport != "http" {
		return fmt.Errorf("%w: unsupported transport %q; only http is supported", ErrInvalidManifest, m.Transport)
	}
	if m.Mode != "video" && m.Mode != "audio-only" {
		return fmt.Errorf("%w: invalid mode %q", ErrInvalidManifest, m.Mode)
	}

	switch m.Stage {
	case JobStagePlanned, JobStageDownloading, JobStageProcessing, JobStageVerifying, JobStageReadyToCommit, JobStageCompleted:
	default:
		return fmt.Errorf("%w: invalid stage %q", ErrInvalidManifest, m.Stage)
	}

	if len(m.Streams) == 0 {
		return fmt.Errorf("%w: manifest has no streams", ErrInvalidManifest)
	}

	for i, s := range m.Streams {
		if s.Index != i {
			return fmt.Errorf("%w: stream index mismatch at %d", ErrInvalidManifest, i)
		}
		if strings.TrimSpace(s.Format.ID) == "" {
			return fmt.Errorf("%w: stream %d missing format ID", ErrInvalidManifest, i)
		}
		if _, err := ValidateJobSubpath(jobDir, s.RelativePath); err != nil {
			return err
		}
	}

	if m.PreCommit != nil && m.PreCommit.StagedRelativePath != "" {
		if _, err := ValidateJobSubpath(jobDir, m.PreCommit.StagedRelativePath); err != nil {
			return err
		}
	}

	return nil
}

// ValidateJobSubpath checks that relPath is a relative subpath strictly within jobDir,
// rejecting absolute paths, directory traversal, symlinks, and non-regular files.
func ValidateJobSubpath(jobDir, relPath string) (string, error) {
	if relPath == "" {
		return "", fmt.Errorf("%w: empty relative path", ErrInvalidJobPath)
	}

	if filepath.IsAbs(relPath) || strings.HasPrefix(relPath, "/") || strings.HasPrefix(relPath, "\\") {
		return "", fmt.Errorf("%w: path %q must be relative", ErrInvalidJobPath, relPath)
	}

	clean := filepath.Clean(relPath)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: path %q attempts traversal", ErrInvalidJobPath, relPath)
	}

	absJobDir, err := filepath.Abs(jobDir)
	if err != nil {
		return "", err
	}

	target := filepath.Join(absJobDir, clean)
	rel, err := filepath.Rel(absJobDir, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: path %q escapes job directory", ErrInvalidJobPath, relPath)
	}

	if linfo, err := os.Lstat(target); err == nil {
		if linfo.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%w: symlinked file %q is rejected", ErrInvalidJobPath, relPath)
		}
		if !linfo.Mode().IsRegular() && !linfo.IsDir() {
			return "", fmt.Errorf("%w: irregular file %q is rejected", ErrInvalidJobPath, relPath)
		}
	}

	return target, nil
}

// ComputeFileSHA256 returns the SHA-256 hex digest and size of path.
func ComputeFileSHA256(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	if !info.Mode().IsRegular() {
		return "", 0, errors.New("goyt: not a regular file")
	}

	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}

	return hex.EncodeToString(h.Sum(nil)), n, nil
}
