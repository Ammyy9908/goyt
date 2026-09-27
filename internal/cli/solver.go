package cli

import (
	"strings"

	"github.com/ammyy9908/goyt/extractor/youtube"
	"github.com/ammyy9908/goyt/extractor/youtube/jssolver"
)

// createChallengeSolver instantiates a ChallengeSolver based on the runtime string.
// If runtime is "" or "none", it returns nil, nil (solver disabled).
// If a valid runtime name or path is provided, it validates the runtime and returns a configured solver.
func createChallengeSolver(runtime string) (youtube.ChallengeSolver, error) {
	runtime = strings.TrimSpace(runtime)
	if runtime == "" || strings.EqualFold(runtime, "none") {
		return nil, nil
	}
	return jssolver.New(jssolver.WithRuntime(runtime))
}
