package jinn

import (
	"fmt"
	"os"
	"strings"
	"time"
)

func (e *Engine) statFile(args map[string]interface{}) string {
	path, _ := args["path"].(string)
	resolved, err := e.checkPath(path)
	if err != nil {
		return fmt.Sprintf("[blocked: %s]", err)
	}

	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("[error: file not found: %s]", path)
		}
		return fmt.Sprintf("[error: %s]", err)
	}

	ftype := "file"
	if info.IsDir() {
		ftype = "directory"
	} else if !info.Mode().IsRegular() {
		ftype = "special"
	}

	lines := 0
	if info.Mode().IsRegular() && info.Size() <= maxFileSize {
		if data, err := os.ReadFile(resolved); err == nil {
			lines = strings.Count(string(data), "\n")
			if len(data) > 0 && data[len(data)-1] != '\n' {
				lines++
			}
		}
	}

	return fmt.Sprintf("path: %s\ntype: %s\nsize: %d bytes\nlines: %d\nmodified: %s",
		path, ftype, info.Size(), lines, info.ModTime().Format(time.RFC3339))
}
