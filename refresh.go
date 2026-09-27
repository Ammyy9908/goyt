package goyt

import (
	"errors"
	"fmt"
	"strings"
)

// MatchRefreshedFormat locates the format in candidates that represents the same logical
// representation as original, ensuring format ID, container, codecs, dimensions, and
// audio track identity (language, original marker, track ID) are strictly preserved.
func MatchRefreshedFormat(original Format, candidates []Format) (*Format, error) {
	var matches []Format
	for _, f := range candidates {
		if f.ID != original.ID {
			continue
		}
		if f.Protocol != original.Protocol {
			continue
		}
		if normalizeContainer(f.Container) != normalizeContainer(original.Container) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(f.VideoCodec), strings.TrimSpace(original.VideoCodec)) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(f.AudioCodec), strings.TrimSpace(original.AudioCodec)) {
			continue
		}
		if original.Height != nil {
			if f.Height == nil || *f.Height != *original.Height {
				continue
			}
		}
		if original.Width != nil {
			if f.Width == nil || *f.Width != *original.Width {
				continue
			}
		}
		if original.AudioTrackID != "" {
			if f.AudioTrackID != original.AudioTrackID {
				continue
			}
		}
		if original.Language != "" {
			if !strings.EqualFold(strings.TrimSpace(f.Language), strings.TrimSpace(original.Language)) {
				continue
			}
		}
		if original.AudioIsOriginal != f.AudioIsOriginal {
			continue
		}
		if original.AudioIsDefault != f.AudioIsDefault {
			continue
		}
		matches = append(matches, f)
	}

	if len(matches) == 0 {
		return nil, fmt.Errorf("goyt: format %s not found in refreshed media", original.ID)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("goyt: ambiguous refreshed format for %s", original.ID)
	}

	cloned := cloneFormat(matches[0])
	return &cloned, nil
}

// MatchRefreshedVariant locates the variant in master that matches the original variant's
// logical properties (resolution, codecs, frame rate, video range, and audio group presence).
func MatchRefreshedVariant(original HLSVariant, master *HLSMaster) (*HLSVariant, error) {
	if master == nil {
		return nil, errors.New("goyt: refreshed master playlist is nil")
	}

	var matches []HLSVariant
	for _, v := range master.Variants {
		if v.Height != original.Height || v.Width != original.Width {
			continue
		}
		if v.Codecs != original.Codecs {
			continue
		}
		if (original.AudioGroup != "") != (v.AudioGroup != "") {
			continue
		}
		if original.FrameRate != v.FrameRate {
			continue
		}
		if original.VideoRange != v.VideoRange {
			continue
		}
		matches = append(matches, v)
	}

	if len(matches) == 0 {
		return nil, fmt.Errorf("goyt: variant matching resolution %dx%d, codecs %q, frame-rate %q, video-range %q not found in refreshed master",
			original.Width, original.Height, original.Codecs, original.FrameRate, original.VideoRange)
	}

	if len(matches) > 1 {
		var bwMatches []HLSVariant
		for _, v := range matches {
			if v.Bandwidth == original.Bandwidth {
				bwMatches = append(bwMatches, v)
			}
		}
		if len(bwMatches) == 1 {
			copy := bwMatches[0]
			return &copy, nil
		}
		return nil, fmt.Errorf("goyt: ambiguous refreshed variant for resolution %dx%d, codecs %q, frame-rate %q, video-range %q",
			original.Width, original.Height, original.Codecs, original.FrameRate, original.VideoRange)
	}

	copy := matches[0]
	return &copy, nil
}

// MatchRefreshedAudioRendition locates the audio rendition in master that matches the original rendition's
// metadata (name, language, default, autoselect). If targetGroup is non-empty, matching is restricted
// to renditions within that group. If targetGroup is empty, matching checks for unambiguous resolution
// across groups.
func MatchRefreshedAudioRendition(original HLSAudioRendition, master *HLSMaster, targetGroup string) (*HLSAudioRendition, error) {
	if master == nil {
		return nil, errors.New("goyt: refreshed master playlist is nil")
	}

	var candidates []HLSAudioRendition
	if targetGroup != "" {
		for _, a := range master.Audio {
			if a.GroupID == targetGroup {
				candidates = append(candidates, a)
			}
		}
	} else if original.GroupID != "" {
		for _, a := range master.Audio {
			if a.GroupID == original.GroupID {
				candidates = append(candidates, a)
			}
		}
		if len(candidates) == 0 {
			candidates = master.Audio
		}
	} else {
		candidates = master.Audio
	}

	var matches []HLSAudioRendition
	groupSet := make(map[string]struct{})
	for _, a := range candidates {
		if a.URL == "" {
			continue
		}
		if a.Name != original.Name {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(a.Language), strings.TrimSpace(original.Language)) {
			continue
		}
		if a.Default != original.Default {
			continue
		}
		if a.AutoSelect != original.AutoSelect {
			continue
		}
		matches = append(matches, a)
		groupSet[a.GroupID] = struct{}{}
	}

	if len(matches) == 0 {
		return nil, fmt.Errorf("goyt: audio rendition %q (%s) not found in refreshed master", original.Name, original.Language)
	}

	if len(groupSet) > 1 {
		return nil, fmt.Errorf("goyt: ambiguous audio rendition %q (%s) found across multiple audio groups in refreshed master", original.Name, original.Language)
	}

	if len(matches) > 1 {
		return nil, fmt.Errorf("goyt: ambiguous refreshed audio rendition %q (%s)", original.Name, original.Language)
	}

	copy := matches[0]
	return &copy, nil
}
