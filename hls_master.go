package goyt

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

type HLSAudioRendition struct {
	GroupID    string
	Name       string
	Language   string
	URL        string
	Default    bool
	AutoSelect bool
}
type HLSVariant struct {
	URL        string
	Width      int
	Height     int
	Bandwidth  int64
	Codecs     string
	AudioGroup string
}

type HLSMaster struct {
	Variants []HLSVariant
	Audio    []HLSAudioRendition
}

type HLSSelection struct {
	Variant         HLSVariant
	Audio           *HLSAudioRendition
	AudioIsOriginal bool
	AudioWarning    string
}

// ParseHLSMaster parses a limited master-playlist subset.
//
// Alternate audio declarations are recognized, but variants referencing
// audio groups are not selectable by this implementation.
func ParseHLSMaster(data []byte, base *url.URL) (*HLSMaster, error) {
	if len(data) > maxHLSPlaylistBytes {
		return nil, errors.New("goyt: HLS master exceeds size limit")
	}

	if !utf8.Valid(data) ||
		bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		return nil, errors.New("goyt: invalid HLS text encoding")
	}

	if !validHLSURL(base) {
		return nil, errors.New("goyt: invalid master playlist URL")
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), maxHLSPlaylistBytes)

	if !scanner.Scan() ||
		strings.TrimSuffix(scanner.Text(), "\r") != "#EXTM3U" {
		return nil, errors.New("goyt: missing EXTM3U header")
	}

	master := &HLSMaster{}
	var pending *HLSVariant

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if !strings.HasPrefix(line, "#") {
			if pending == nil {
				return nil, errors.New("goyt: variant URI without STREAM-INF")
			}

			reference, err := url.Parse(line)
			if err != nil {
				return nil, errors.New("goyt: invalid variant URI")
			}

			resolved := base.ResolveReference(reference)
			if !validHLSURL(resolved) ||
				(base.Scheme == "https" && resolved.Scheme != "https") {
				return nil, errors.New("goyt: unsupported variant URL")
			}

			pending.URL = resolved.String()
			master.Variants = append(master.Variants, *pending)
			pending = nil

			if len(master.Variants) > 1000 {
				return nil, errors.New("goyt: too many HLS variants")
			}
			continue
		}

		tag, value, _ := strings.Cut(line, ":")

		switch tag {
		case "#EXT-X-STREAM-INF":
			if pending != nil {
				return nil, errors.New("goyt: previous variant is missing its URI")
			}

			attributes, err := parseHLSAttributes(value)
			if err != nil {
				return nil, err
			}

			bandwidth, err := strconv.ParseInt(
				attributes["BANDWIDTH"], 10, 64,
			)
			if err != nil || bandwidth <= 0 {
				return nil, errors.New("goyt: invalid variant bandwidth")
			}

			variant := HLSVariant{
				Bandwidth:  bandwidth,
				Codecs:     attributes["CODECS"],
				AudioGroup: attributes["AUDIO"],
			}

			if resolution := attributes["RESOLUTION"]; resolution != "" {
				widthText, heightText, ok := strings.Cut(resolution, "x")
				width, widthErr := strconv.Atoi(widthText)
				height, heightErr := strconv.Atoi(heightText)

				if !ok || widthErr != nil || heightErr != nil ||
					width <= 0 || height <= 0 {
					return nil, errors.New("goyt: invalid variant resolution")
				}

				variant.Width = width
				variant.Height = height
			}

			// Alternate video and subtitles need explicit rendition handling.
			if attributes["VIDEO"] != "" {
				return nil, errors.New(
					"goyt: alternate video renditions are not supported yet",
				)
			}

			pending = &variant

		case "#EXT-X-MEDIA":
			attributes, err := parseHLSAttributes(value)
			if err != nil {
				return nil, err
			}

			switch attributes["TYPE"] {
			case "AUDIO":
				audio, err := parseAudioRendition(attributes, base)
				if err != nil {
					return nil, err
				}

				for _, previous := range master.Audio {
					if previous.GroupID == audio.GroupID &&
						previous.Name == audio.Name {
						return nil, errors.New(
							"goyt: duplicate audio rendition name within group",
						)
					}

					if previous.GroupID == audio.GroupID &&
						previous.Default && audio.Default {
						return nil, errors.New(
							"goyt: multiple default audio renditions within group",
						)
					}
				}

				master.Audio = append(master.Audio, audio)

			case "SUBTITLES", "CLOSED-CAPTIONS", "VIDEO":
				// These rendition types are not selected by this implementation.

			default:
				return nil, errors.New("goyt: invalid rendition type")
			}

		case "#EXT-X-VERSION":
			version, err := strconv.Atoi(value)
			if err != nil || version < 1 || version > 7 {
				return nil, errors.New("goyt: unsupported HLS version")
			}

		case "#EXT-X-INDEPENDENT-SEGMENTS":
			// Does not change variant selection.

		default:
			if strings.HasPrefix(tag, "#EXT") {
				return nil, fmt.Errorf(
					"goyt: unsupported master playlist tag: %s", tag,
				)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if pending != nil || len(master.Variants) == 0 {
		return nil, errors.New("goyt: incomplete or empty master playlist")
	}

	return master, nil
}

// Select chooses the highest known resolution within maxHeight.
//
// Equal heights prefer higher advertised bandwidth. Remaining ties preserve
// playlist order. Bandwidth is not a guarantee of visual quality.
func (m *HLSMaster) Select(maxHeight int) (*HLSVariant, error) {
	if maxHeight <= 0 {
		return nil, errors.New("goyt: maximum height must be positive")
	}

	var best *HLSVariant

	for _, variant := range m.Variants {
		if variant.Height <= 0 || variant.Height > maxHeight {
			continue
		}

		if variant.AudioGroup != "" {
			continue
		}

		if !hlsCombinedH264AAC(variant.Codecs) {
			continue
		}

		if best == nil ||
			variant.Height > best.Height ||
			(variant.Height == best.Height &&
				variant.Bandwidth > best.Bandwidth) {
			copy := variant
			best = &copy
		}
	}

	if best == nil {
		return nil, errors.New(
			"goyt: no compatible combined H.264/AAC variant within height limit; " +
				"audio rendition groups and unknown codec/resolution metadata are unsupported",
		)
	}

	return best, nil
}

func parseAudioRendition(
	attributes map[string]string,
	base *url.URL,
) (HLSAudioRendition, error) {
	audio := HLSAudioRendition{
		GroupID:  attributes["GROUP-ID"],
		Name:     attributes["NAME"],
		Language: attributes["LANGUAGE"],
	}

	if audio.GroupID == "" || audio.Name == "" {
		return audio, errors.New(
			"goyt: audio rendition requires GROUP-ID and NAME",
		)
	}

	for _, key := range []string{"DEFAULT", "AUTOSELECT"} {
		value := attributes[key]
		if value != "" && value != "YES" && value != "NO" {
			return audio, fmt.Errorf(
				"goyt: invalid audio rendition %s", key,
			)
		}
	}

	audio.Default = attributes["DEFAULT"] == "YES"
	audio.AutoSelect = attributes["AUTOSELECT"] == "YES"

	if audio.Default && attributes["AUTOSELECT"] == "NO" {
		return audio, errors.New(
			"goyt: default audio cannot set AUTOSELECT=NO",
		)
	}

	if reference := attributes["URI"]; reference != "" {
		parsed, err := url.Parse(reference)
		if err != nil {
			return audio, errors.New("goyt: invalid audio rendition URI")
		}

		resolved := base.ResolveReference(parsed)
		if !validHLSURL(resolved) ||
			(base.Scheme == "https" && resolved.Scheme != "https") {
			return audio, errors.New("goyt: unsupported audio rendition URL")
		}

		audio.URL = resolved.String()
	}

	// No URI means in-band audio. Keep the declaration, but the new selector
	// will not select that grouped configuration yet.
	return audio, nil
}

// SelectWithAudio chooses a combined variant or a variant with a separate
// MPEG-TS audio playlist.
//
// CODECS on STREAM-INF describes the overall variant, including renditions.
// This subset requires one H.264 codec and one AAC codec in that declaration.
func (m *HLSMaster) SelectWithAudio(
	maxHeight int,
) (*HLSSelection, error) {
	return m.SelectWithAudioLanguage(maxHeight, "")
}

// SelectWithAudioLanguage requires a matching external audio rendition when
// language is nonempty. Unknown-language combined streams are then excluded.
func (m *HLSMaster) SelectWithAudioLanguage(
	maxHeight int,
	language string,
) (*HLSSelection, error) {
	if m == nil {
		return nil, errors.New("goyt: HLS master is nil")
	}
	if maxHeight <= 0 {
		return nil, errors.New("goyt: maximum height must be positive")
	}

	language = strings.ToLower(strings.TrimSpace(language))
	var best *HLSSelection

	for _, variant := range m.Variants {
		if variant.Height <= 0 ||
			variant.Height > maxHeight ||
			!hlsCombinedH264AAC(variant.Codecs) {
			continue
		}

		var audio *HLSAudioRendition
		var isOriginal bool
		var warning string

		if variant.AudioGroup != "" {
			audio, isOriginal, warning = m.selectAudioLanguage(variant.AudioGroup, language)
			if audio == nil {
				continue
			}
		} else if language != "" {
			// No rendition metadata establishes the embedded audio language.
			continue
		}

		if best == nil ||
			variant.Height > best.Variant.Height ||
			(variant.Height == best.Variant.Height &&
				variant.Bandwidth > best.Variant.Bandwidth) {
			best = &HLSSelection{
				Variant:         variant,
				Audio:           audio,
				AudioIsOriginal: isOriginal,
				AudioWarning:    warning,
			}
		}
	}

	if best == nil {
		if language != "" {
			return nil, fmt.Errorf(
				"goyt: no supported H.264/AAC variant up to %dp "+
					"with external audio matching %q",
				maxHeight,
				language,
			)
		}

		return nil, errors.New(
			"goyt: no supported H.264/AAC variant and audio combination within height limit",
		)
	}

	return best, nil
}

// Keep this wrapper for existing internal callers and tests.
func (m *HLSMaster) selectAudio(group string) *HLSAudioRendition {
	audio, _, _ := m.selectAudioLanguage(group, "")
	return audio
}

func (m *HLSMaster) selectAudioLanguage(
	group string,
	language string,
) (*HLSAudioRendition, bool, string) {
	language = strings.ToLower(strings.TrimSpace(language))

	var candidates []HLSAudioRendition
	for _, audio := range m.Audio {
		if audio.GroupID == group && audio.URL != "" {
			candidates = append(candidates, audio)
		}
	}

	if len(candidates) == 0 {
		return nil, false, ""
	}

	// 1. Explicit language requested.
	if language != "" {
		var selected *HLSAudioRendition
		bestLangRank := -1
		bestPrefRank := -1
		isOriginal := false

		for _, audio := range candidates {
			langRank := hlsLanguageRank(audio.Language, language)
			if langRank < 0 {
				continue
			}

			prefRank := 0
			if isOriginalAudioName(audio.Name) {
				prefRank += 10
			}
			if audio.Default {
				prefRank += 4
			}
			if audio.AutoSelect {
				prefRank += 2
			}
			if !isDubbedAudioName(audio.Name) {
				prefRank += 1
			}

			if selected == nil ||
				langRank > bestLangRank ||
				(langRank == bestLangRank && prefRank > bestPrefRank) {
				copy := audio
				selected = &copy
				bestLangRank = langRank
				bestPrefRank = prefRank
				isOriginal = isOriginalAudioName(audio.Name)
			}
		}

		return selected, isOriginal, ""
	}

	// 2. Automatic selection: Prefer original audio based on evidence.
	var originalCandidates []HLSAudioRendition
	for _, audio := range candidates {
		if isOriginalAudioName(audio.Name) {
			originalCandidates = append(originalCandidates, audio)
		}
	}

	if len(originalCandidates) > 0 {
		var selected *HLSAudioRendition
		bestPref := -1
		for _, audio := range originalCandidates {
			pref := 0
			if audio.Default {
				pref += 4
			}
			if audio.AutoSelect {
				pref += 2
			}
			if selected == nil || pref > bestPref {
				copy := audio
				selected = &copy
				bestPref = pref
			}
		}
		warning := ""
		if len(originalCandidates) > 1 {
			warning = fmt.Sprintf(
				"goyt: multiple audio renditions are marked original in group %q; "+
					"selected %q using default/autoselect preference and playlist order",
				group,
				selected.Name,
			)
		}
		return selected, true, warning
	}

	// 3. No original audio evidence found. Fall back to playlist default.
	var selected *HLSAudioRendition
	bestPref := -1
	for _, audio := range candidates {
		pref := 0
		if audio.Default {
			pref += 8
		}
		if audio.AutoSelect {
			pref += 4
		}
		if !isDubbedAudioName(audio.Name) {
			pref += 2
		}

		if selected == nil || pref > bestPref {
			copy := audio
			selected = &copy
			bestPref = pref
		}
	}

	warning := ""
	if selected != nil {
		warning = fmt.Sprintf(
			"goyt: original audio track could not be established from metadata; selecting fallback audio %q (%s)",
			selected.Name,
			selected.Language,
		)
	}

	return selected, false, warning
}

// Recognizes explicit name markers, not verified audio provenance.
// Unrecognized/localized markers remain unknown.
func isOriginalAudioName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))

	if lower == "" || isDubbedAudioName(lower) {
		return false
	}

	if lower == "original" {
		return true
	}

	for _, suffix := range []string{
		" - original",
		" (original)",
		" [original]",
	} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}

	return lower == "(original)" || lower == "[original]"
}

func isDubbedAudioName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return false
	}
	return strings.Contains(lower, "dubbed") ||
		strings.Contains(lower, "dubbed-auto") ||
		strings.Contains(lower, "(dubbed)") ||
		strings.Contains(lower, "[dubbed]") ||
		strings.Contains(lower, "- dubbed")
}

func hlsLanguageRank(actual, requested string) int {
	actual = strings.ToLower(strings.TrimSpace(actual))
	requested = strings.ToLower(strings.TrimSpace(requested))

	if requested == "" {
		return 0
	}
	if actual == "" {
		return -1
	}
	if actual == requested {
		return 2
	}
	if !strings.Contains(requested, "-") &&
		strings.HasPrefix(actual, requested+"-") {
		return 1
	}
	return -1
}
func hlsCombinedH264AAC(codecs string) bool {
	var video, audio int

	for _, codec := range strings.Split(codecs, ",") {
		switch normalizeCodec(codec) {
		case "h264":
			video++
		case "aac":
			audio++
		default:
			return false
		}
	}

	return video == 1 && audio == 1
}

// parseHLSAttributes handles commas inside quoted values, such as CODECS.
// HLS quoted strings do not use backslash escape syntax.
func parseHLSAttributes(input string) (map[string]string, error) {
	result := make(map[string]string)

	for input != "" {
		key, remaining, ok := strings.Cut(input, "=")
		key = strings.TrimSpace(key)

		if !ok || key == "" {
			return nil, errors.New("goyt: malformed HLS attribute")
		}

		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("goyt: duplicate HLS attribute %s", key)
		}

		remaining = strings.TrimSpace(remaining)
		var value string

		if strings.HasPrefix(remaining, `"`) {
			end := strings.IndexByte(remaining[1:], '"')
			if end < 0 {
				return nil, errors.New("goyt: unclosed HLS quoted value")
			}

			value = remaining[1 : end+1]
			remaining = strings.TrimSpace(remaining[end+2:])

			if remaining != "" && !strings.HasPrefix(remaining, ",") {
				return nil, errors.New("goyt: invalid HLS attribute separator")
			}
		} else {
			value, remaining, _ = strings.Cut(remaining, ",")
			value = strings.TrimSpace(value)

			if value == "" {
				return nil, errors.New("goyt: empty HLS attribute")
			}

			result[key] = value
			input = strings.TrimSpace(remaining)
			continue
		}

		result[key] = value

		if remaining == "" {
			input = ""
		} else {
			input = strings.TrimSpace(remaining[1:])
			if input == "" {
				return nil, errors.New("goyt: trailing attribute separator")
			}
		}
	}

	return result, nil
}

func isHLSMaster(data []byte) bool {
	for _, line := range bytes.Split(data, []byte("\n")) {
		tag, _, _ := strings.Cut(strings.TrimSpace(string(line)), ":")
		if tag == "#EXT-X-STREAM-INF" || tag == "#EXT-X-MEDIA" {
			return true
		}
	}
	return false
}
