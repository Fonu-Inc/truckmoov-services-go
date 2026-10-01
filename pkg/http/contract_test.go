package httpx

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Every Go service answers through response.go. A handler writing JSON, an
// error or a status line itself is how one service drifts into its own shape,
// so any such write fails here. A write that genuinely must stay raw (HTML, a
// webhook challenge) carries a "contract:raw <reason>" comment on its line.
var rawWrite = regexp.MustCompile(`json\.NewEncoder\(w\)|\bw\.Write\(|http\.Error\(|http\.NotFound\(|\.WriteHeader\(`)

func TestHandlersAnswerThroughTheContract(t *testing.T) {
	root := filepath.Join("..", "..", "internal")
	var offences []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		if !strings.Contains(filepath.ToSlash(path), "/interfaces/http/") {
			return nil
		}
		source, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for number, line := range strings.Split(string(source), "\n") {
			if rawWrite.MatchString(line) && !strings.Contains(line, "contract:raw") {
				offences = append(offences, filepath.ToSlash(path)+":"+strconv.Itoa(number+1)+": "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk handlers: %v", err)
	}
	if len(offences) > 0 {
		t.Fatalf("write through httpx.WriteOK/WriteSuccess/WriteError/WriteDocument instead:\n%s", strings.Join(offences, "\n"))
	}
}
