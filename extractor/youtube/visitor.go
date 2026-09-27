package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
)

var ytcfgAssignment = regexp.MustCompile(`\bytcfg\.set\s*\(\s*`)

type visitorFields struct {
	VisitorData string `json:"VISITOR_DATA"`

	InnerTubeContext struct {
		Client struct {
			VisitorData string `json:"visitorData"`
		} `json:"client"`
	} `json:"INNERTUBE_CONTEXT"`

	ResponseContext struct {
		VisitorData string `json:"visitorData"`
	} `json:"responseContext"`
}

func parseVisitorData(page []byte) (string, error) {
	var config visitorFields

	// A page can contain several ytcfg.set calls.
	// Decode into the same struct to retain fields across partial configs.
	for _, match := range ytcfgAssignment.FindAllIndex(page, -1) {
		candidate := config

		decoder := json.NewDecoder(
			bytes.NewReader(page[match[1]:]),
		)

		if err := decoder.Decode(&candidate); err == nil {
			config = candidate
		}
	}

	if config.VisitorData != "" {
		return config.VisitorData, nil
	}

	if value := config.InnerTubeContext.Client.VisitorData; value != "" {
		return value, nil
	}

	// Fall back to responseContext in the embedded player response.
	for _, match := range playerAssignment.FindAllIndex(page, -1) {
		var fields visitorFields

		decoder := json.NewDecoder(
			bytes.NewReader(page[match[1]:]),
		)

		if err := decoder.Decode(&fields); err != nil {
			continue
		}

		if value := fields.ResponseContext.VisitorData; value != "" {
			return value, nil
		}
	}

	return "", errors.New(
		"youtube: watch page did not expose visitor data",
	)
}

func (e *Extractor) fetchVisitorData(
	ctx context.Context,
	id string,
) (string, error) {
	if !videoIDPattern.MatchString(id) {
		return "", errors.New("youtube: invalid video ID")
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		"https://www.youtube.com/watch?v="+id+"&hl=en",
		nil,
	)
	if err != nil {
		return "", err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	// Use an unauthenticated request, consistent with the current player call.
	client := *e.client
	client.Jar = nil

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("youtube: fetch visitor context: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf(
			"youtube: visitor page returned HTTP %d",
			resp.StatusCode,
		)
	}

	page, err := io.ReadAll(
		io.LimitReader(resp.Body, maxPageBytes+1),
	)
	if err != nil {
		return "", err
	}

	if len(page) > maxPageBytes {
		return "", errors.New("youtube: visitor page exceeds size limit")
	}

	if err := ctx.Err(); err != nil {
		return "", err
	}

	return parseVisitorData(page)
}
