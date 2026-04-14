package utils

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

var templateDataQuotedRE = regexp.MustCompile(`^(\s*data\s*=\s*)"((?:[^"\\]|\\.)*)"\s*$`)

// PrettyPrintHCL formats HCL and rewrites multiline template data strings as
// heredocs for readability.
func PrettyPrintHCL(src string) string {
	formatted := string(hclwrite.Format([]byte(src)))

	lines := strings.Split(formatted, "\n")
	out := make([]string, 0, len(lines)+8)

	for _, line := range lines {
		m := templateDataQuotedRE.FindStringSubmatch(line)
		if len(m) != 3 {
			out = append(out, line)
			continue
		}

		decoded, err := strconv.Unquote(`"` + m[2] + `"`)
		if err != nil || !strings.Contains(decoded, "\n") {
			out = append(out, line)
			continue
		}

		marker := heredocMarker(decoded)
		out = append(out, m[1]+"<<-"+marker)

		contentLines := strings.Split(decoded, "\n")
		if len(contentLines) > 0 && contentLines[len(contentLines)-1] == "" {
			contentLines = contentLines[:len(contentLines)-1]
		}
		out = append(out, contentLines...)
		out = append(out, marker)
	}

	return strings.Join(out, "\n")
}

func heredocMarker(content string) string {
	marker := "EOT"
	for strings.Contains(content, "\n"+marker+"\n") || strings.HasPrefix(content, marker+"\n") || strings.HasSuffix(content, "\n"+marker) || content == marker {
		marker += "X"
	}
	return marker
}
