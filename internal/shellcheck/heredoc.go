package shellcheck

import "strings"

// ExtractHCLHeredoc returns the body of a `data = <<-EOT ... EOT` (or `<<EOT`)
// template block whose `destination` matches `local/<destName>`. The returned
// body is dedented (matching `<<-EOT` semantics) and HCL2 escapes are
// un-escaped (`$${...}` → `${...}`, `%%{` → `%{`) so callers receive the
// script as bash will see it after Nomad's HCL parser is done.
//
// Returns "" when the destination is not found. Used by generator tests to
// pull emitted scripts back out of rendered HCL for [Parse] / [Lint] checks.
func ExtractHCLHeredoc(hcl, destName string) string {
	body := findHeredocBody(hcl, "local/"+destName)
	if body == "" {
		return ""
	}
	return strings.NewReplacer(`$${`, `${`, `%%{`, `%{`).Replace(dedent(body))
}

// findHeredocBody parses the rendered HCL into discrete `template { ... }`
// blocks (tracking brace depth, treating heredoc bodies as opaque) and
// returns the body of the heredoc whose block declares the matching
// destination.
func findHeredocBody(hcl, destPath string) string {
	dest := `destination = "` + destPath + `"`
	for _, blk := range templateBlocks(hcl) {
		if !strings.Contains(blk, dest) {
			continue
		}
		body, ok := heredocBody(blk)
		if ok {
			return body
		}
	}
	return ""
}

// templateBlocks returns the contents of each top-level `template {...}` block
// in the rendered HCL, brace-balanced and skipping any `{`/`}` that appear
// inside a `<<-EOT ... EOT` heredoc body.
func templateBlocks(hcl string) []string {
	lines := strings.Split(hcl, "\n")
	var blocks []string
	for i := 0; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(t, "template {") && !strings.HasPrefix(t, "template{") {
			continue
		}
		depth := 1
		inHeredoc := false
		var buf strings.Builder
		for j := i + 1; j < len(lines); j++ {
			ln := lines[j]
			tj := strings.TrimSpace(ln)
			if inHeredoc {
				buf.WriteString(ln)
				buf.WriteByte('\n')
				if tj == "EOT" {
					inHeredoc = false
				}
				continue
			}
			if isHeredocOpen(tj) {
				buf.WriteString(ln)
				buf.WriteByte('\n')
				inHeredoc = true
				continue
			}
			depth += strings.Count(tj, "{")
			depth -= strings.Count(tj, "}")
			if depth <= 0 {
				blocks = append(blocks, buf.String())
				i = j
				break
			}
			buf.WriteString(ln)
			buf.WriteByte('\n')
		}
	}
	return blocks
}

// heredocBody returns the body of the first `data = <<-EOT ... EOT` (or `<<EOT`)
// pair found in block.
func heredocBody(block string) (string, bool) {
	lines := strings.Split(block, "\n")
	for i, ln := range lines {
		if !isHeredocOpen(strings.TrimSpace(ln)) {
			continue
		}
		for k := i + 1; k < len(lines); k++ {
			if strings.TrimSpace(lines[k]) == "EOT" {
				return strings.Join(lines[i+1:k], "\n"), true
			}
		}
	}
	return "", false
}

func isHeredocOpen(line string) bool {
	return strings.HasPrefix(line, "data") && strings.Contains(line, "<<") && strings.HasSuffix(line, "EOT")
}

func dedent(s string) string {
	lines := strings.Split(s, "\n")
	common := -1
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		n := len(ln) - len(strings.TrimLeft(ln, " \t"))
		if common < 0 || n < common {
			common = n
		}
	}
	if common <= 0 {
		return s
	}
	for i, ln := range lines {
		if len(ln) >= common {
			lines[i] = ln[common:]
		}
	}
	return strings.Join(lines, "\n")
}
