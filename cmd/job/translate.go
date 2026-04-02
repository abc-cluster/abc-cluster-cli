package job

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kballard/go-shellquote"
	"github.com/spf13/cobra"
)

type schedulerTranslator interface {
	Name() string
	Detect(preamble []string) bool
	TranslateLine(line string) (abc []string, unmappedOrNote []string)
}

var translators = []schedulerTranslator{
	&slurmTranslator{},
	&pbsTranslator{},
}

func newTranslateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "translate <script>",
		Short: "Translate a SLURM or PBS job script to ABC directives",
		Long: `Translate a scheduler script from SLURM or PBS into a script
annotated with #ABC directives, preserving unmapped directives with notes.

Output is plain translated script (not HCL).`,
		Args: cobra.ExactArgs(1),
		RunE: runTranslateCmd,
	}

	cmd.Flags().String("out", "", "Write translated script to file (default stdout)")
	cmd.Flags().Bool("strict", false, "Fail when an unmapped directive is found")
	cmd.Flags().String("executor", "", "Force scheduler type: slurm,pbs")
	return cmd
}

func runTranslateCmd(cmd *cobra.Command, args []string) error {
	scriptPath := args[0]
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		return fmt.Errorf("cannot read script %q: %w", scriptPath, err)
	}

	header := filepath.Base(scriptPath)
	executor, _ := cmd.Flags().GetString("executor")
	translated, unmapped, err := translateScript(string(content), header, executor)
	if err != nil {
		return err
	}

	strict, _ := cmd.Flags().GetBool("strict")
	if strict && len(unmapped) > 0 {
		return fmt.Errorf("strict translation failed: %d unmapped directives", len(unmapped))
	}

	outFile, _ := cmd.Flags().GetString("out")
	if outFile != "" {
		if err := os.WriteFile(outFile, []byte(translated), 0644); err != nil {
			return fmt.Errorf("cannot write output file %q: %w", outFile, err)
		}
		return nil
	}

	fmt.Fprint(cmd.OutOrStdout(), translated)

	if len(unmapped) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "WARNING: some directives could not be mapped to ABC:")
		for _, u := range unmapped {
			fmt.Fprintf(cmd.ErrOrStderr(), "  - %s\n", u)
		}
	}

	return nil
}

func translateScript(content, scriptName, executor string) (string, []string, error) {
	lines := strings.Split(content, "\n")
	var shebang string
	start := 0
	if len(lines) > 0 && strings.HasPrefix(lines[0], "#!") {
		shebang = lines[0]
		start = 1
	}

	preamble := []string{}
	body := []string{}
	inPreamble := true
	for i := start; i < len(lines); i++ {
		l := lines[i]
		trim := strings.TrimSpace(l)
		if inPreamble && (trim == "" || strings.HasPrefix(trim, "#")) {
			preamble = append(preamble, l)
			continue
		}
		inPreamble = false
		body = append(body, l)
	}

	translator := detectTranslator(preamble, executor)
	if translator == nil {
		return "", nil, fmt.Errorf("no supported scheduler directives found (supported: slurm, pbs)")
	}

	abcLines := []string{}
	preservedLines := []string{}
	unmapped := []string{}

	// Apply line-by-line translation for scheduler directives.
	for _, l := range preamble {
		trim := strings.TrimSpace(l)
		if strings.HasPrefix(trim, "#SBATCH") || strings.HasPrefix(trim, "#PBS") {
			mapped, notes := translator.TranslateLine(l)
			if len(mapped) > 0 {
				abcLines = append(abcLines, mapped...)
			}
			if len(notes) > 0 {
				for _, note := range notes {
					preservedLines = append(preservedLines, fmt.Sprintf("# NOTE: %s", note))
				}
			}
			if len(mapped) == 0 {
				preservedLines = append(preservedLines, fmt.Sprintf("# NOTE: unmapped directive from %s, preserved", translator.Name()))
				preservedLines = append(preservedLines, trim)
				unmapped = append(unmapped, strings.TrimSpace(trim))
			}
			continue
		}
		preservedLines = append(preservedLines, l)
	}

	outLines := []string{}
	if shebang != "" {
		outLines = append(outLines, shebang)
	}

	if len(abcLines) > 0 {
		outLines = append(outLines, "# ABC translated directives")
		outLines = append(outLines, abcLines...)
	}
	if len(preservedLines) > 0 {
		outLines = append(outLines, "# Preserved directives")
		outLines = append(outLines, preservedLines...)
	}

	if len(body) > 0 {
		if len(outLines) > 0 {
			outLines = append(outLines, "")
		}
		outLines = append(outLines, body...)
	}

	return strings.Join(outLines, "\n"), unmapped, nil
}

func detectTranslator(preamble []string, executor string) schedulerTranslator {
	if executor != "" {
		executor = strings.ToLower(strings.TrimSpace(executor))
		for _, t := range translators {
			if t.Name() == executor {
				return t
			}
		}
		return nil
	}

	for _, t := range translators {
		if t.Detect(preamble) {
			return t
		}
	}
	return nil
}

func slurmDetect(preamble []string) bool {
	for _, l := range preamble {
		if strings.Contains(l, "#SBATCH") {
			return true
		}
	}
	return false
}

func pbsDetect(preamble []string) bool {
	for _, l := range preamble {
		if strings.Contains(l, "#PBS") {
			return true
		}
	}
	return false
}

// slurmTranslator handles #SBATCH translation.

type slurmTranslator struct{}

func (slurmTranslator) Name() string { return "slurm" }
func (slurmTranslator) Detect(preamble []string) bool { return slurmDetect(preamble) }

func (slurmTranslator) TranslateLine(line string) ([]string, []string) {
	trim := strings.TrimSpace(line)
	content := strings.TrimSpace(strings.TrimPrefix(trim, "#SBATCH"))
	if content == "" {
		return nil, nil
	}

	tokens, err := shellquote.Split(content)
	if err != nil {
		return nil, []string{fmt.Sprintf("failed to split SLURM line: %v", err)}
	}
	rez := []string{}
	notes := []string{}
	mappedSomething := false
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		key, value, hasValue, ok := parseOpt(tok)
		if !ok {
			continue
		}
		if !hasValue && i+1 < len(tokens) && !strings.HasPrefix(tokens[i+1], "-") {
			value = tokens[i+1]
			hasValue = true
			i++
		}

		switch key {
		case "job-name", "J":
			if hasValue {
				rez = append(rez, fmt.Sprintf("#ABC --name=%s", shellQuoteArg(value)))
				mappedSomething = true
			}
		case "cpus-per-task":
			if hasValue {
				rez = append(rez, fmt.Sprintf("#ABC --cores=%s", shellQuoteArg(value)))
				mappedSomething = true
			}
		case "mem":
			if hasValue {
				rez = append(rez, fmt.Sprintf("#ABC --mem=%s", shellQuoteArg(value)))
				mappedSomething = true
			}
		case "time":
			if hasValue {
				rez = append(rez, fmt.Sprintf("#ABC --time=%s", shellQuoteArg(value)))
				mappedSomething = true
			}
		case "nodes":
			if hasValue {
				rez = append(rez, fmt.Sprintf("#ABC --nodes=%s", shellQuoteArg(value)))
				mappedSomething = true
			}
		case "ntasks":
			if hasValue {
			notes = append(notes, fmt.Sprintf("SLURM ntasks=%s has no direct ABC translation; preserved as comment", value))
			mappedSomething = false
			}
		case "partition":
			if hasValue {
				rez = append(rez, fmt.Sprintf("#ABC --dc=%s", shellQuoteArg(value)))
				mappedSomething = true
			}
		case "array":
			if hasValue {
				arrayAsNodes, ok := estimateArrayCount(value)
				if ok {
					rez = append(rez, fmt.Sprintf("#ABC --nodes=%d", arrayAsNodes))
					notes = append(notes, fmt.Sprintf("SLURM array=%s translated to nodes=%d", value, arrayAsNodes))
					mappedSomething = true
				} else {
					notes = append(notes, fmt.Sprintf("SLURM array=%s could not map exactly", value))
				}
			}
		default:
			// unknown token, will be preserved by runTranslate.
		}
	}

	if mappedSomething {
		return rez, notes
	}
	return nil, notes
}

// pbsTranslator handles #PBS translation.

type pbsTranslator struct{}

func (pbsTranslator) Name() string { return "pbs" }
func (pbsTranslator) Detect(preamble []string) bool { return pbsDetect(preamble) }

func (pbsTranslator) TranslateLine(line string) ([]string, []string) {
	trim := strings.TrimSpace(line)
	content := strings.TrimSpace(strings.TrimPrefix(trim, "#PBS"))
	if content == "" {
		return nil, nil
	}

	tokens, err := shellquote.Split(content)
	if err != nil {
		return nil, []string{fmt.Sprintf("failed to split PBS line: %v", err)}
	}
	rez := []string{}
	notes := []string{}
	mappedSomething := false

	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if strings.HasPrefix(tok, "-N") {
			name := strings.TrimPrefix(tok, "-N")
			if name == "" && i+1 < len(tokens) {
				i++
				name = tokens[i]
			}
			if name != "" {
				rez = append(rez, fmt.Sprintf("#ABC --name=%s", shellQuoteArg(name)))
				mappedSomething = true
			}
			continue
		}
		if strings.HasPrefix(tok, "-q") {
			queue := strings.TrimPrefix(tok, "-q")
			if queue == "" && i+1 < len(tokens) {
				i++
				queue = tokens[i]
			}
			if queue != "" {
				rez = append(rez, fmt.Sprintf("#ABC --dc=%s", shellQuoteArg(queue)))
				mappedSomething = true
			}
			continue
		}
		if strings.HasPrefix(tok, "-t") {
			array := strings.TrimPrefix(tok, "-t")
			if array == "" && i+1 < len(tokens) {
				i++
				array = tokens[i]
			}
			if array != "" {
				if c, ok := estimateArrayCount(array); ok {
					rez = append(rez, fmt.Sprintf("#ABC --nodes=%d", c))
					mappedSomething = true
					notes = append(notes, fmt.Sprintf("PBS array=%s translated to nodes=%d", array, c))
				} else {
					notes = append(notes, fmt.Sprintf("PBS array=%s requires manual review", array))
				}
			}
			continue
		}
		if strings.HasPrefix(tok, "-l") {
			list := strings.TrimPrefix(tok, "-l")
			if list == "" && i+1 < len(tokens) {
				i++
				list = tokens[i]
			}
			for _, item := range strings.Split(list, ",") {
				if item == "" {
					continue
				}
				kv := strings.SplitN(item, "=", 2)
				if len(kv) != 2 {
					continue
				}
				k := kv[0]
				v := kv[1]
				if k == "walltime" {
					rez = append(rez, fmt.Sprintf("#ABC --time=%s", shellQuoteArg(v)))
					mappedSomething = true
				} else if k == "mem" {
					rez = append(rez, fmt.Sprintf("#ABC --mem=%s", shellQuoteArg(v)))
					mappedSomething = true
				} else if k == "nodes" {
					// nodes may contain ppn. Keep both if possible
					nodes, ppn := parsePBSNodes(v)
					if nodes > 0 {
						rez = append(rez, fmt.Sprintf("#ABC --nodes=%d", nodes))
						mappedSomething = true
					}
					if ppn > 0 {
						rez = append(rez, fmt.Sprintf("#ABC --cores=%d", ppn))
						mappedSomething = true
					}
				}
			}
			continue
		}
	}

	if mappedSomething {
		return rez, notes
	}
	return nil, notes
}

func parseOpt(token string) (key, value string, hasValue, ok bool) {
	if strings.HasPrefix(token, "--") {
		kv := strings.TrimPrefix(token, "--")
		parts := strings.SplitN(kv, "=", 2)
		key = parts[0]
		if len(parts) == 2 {
			value = parts[1]
			hasValue = true
		}
		return key, value, hasValue, true
	}
	if strings.HasPrefix(token, "-") {
		short := strings.TrimPrefix(token, "-")
		if len(short) > 1 {
			key = short[:1]
			value = short[1:]
			hasValue = value != ""
			return key, value, hasValue, true
		}
		key = short
		return key, "", false, true
	}
	return "", "", false, false
}


func shellQuoteArg(val string) string {
	// quote shell argument safely; Join handles escaping as one token.
	return shellquote.Join(val)
}

func estimateArrayCount(expr string) (int, bool) {
	if strings.Contains(expr, "-") {
		parts := strings.Split(expr, "-")
		if len(parts) != 2 {
			return 0, false
		}
		lo, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
		hi, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err1 != nil || err2 != nil || hi < lo {
			return 0, false
		}
		return hi - lo + 1, true
	}
	if strings.Contains(expr, ",") {
		parts := strings.Split(expr, ",")
		return len(parts), true
	}
	n, err := strconv.Atoi(strings.TrimSpace(expr))
	if err == nil {
		return n, true
	}
	return 0, false
}

func parsePBSNodes(expr string) (nodes, ppn int) {
	if strings.Contains(expr, ":") {
		parts := strings.Split(expr, ":")
		n, err := strconv.Atoi(parts[0])
		if err == nil {
			nodes = n
		}
		for _, p := range parts[1:] {
			if strings.HasPrefix(p, "ppn=") {
				v := strings.TrimPrefix(p, "ppn=")
				m, err := strconv.Atoi(v)
				if err == nil {
					ppn = m
				}
			}
		}
	} else {
		n, err := strconv.Atoi(expr)
		if err == nil {
			nodes = n
		}
	}
	return
}
