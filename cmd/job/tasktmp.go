package job

// taskTmpPreambleLines returns shell lines after the shebang so TMPDIR/TMP/TEMP
// point at ${NOMAD_TASK_DIR}/tmp (created with mkdir -p), or nil when disabled.
// FinalizeJobScript places these before the Pixi runtime guard when both apply.
func taskTmpPreambleLines(spec *jobSpec) []string {
	if spec == nil || !spec.TaskTmp {
		return nil
	}
	return []string{
		`# --- abc task-tmp (generated) ---`,
		`mkdir -p "${NOMAD_TASK_DIR}/tmp" || {`,
		`  echo "abc task-tmp: could not mkdir ${NOMAD_TASK_DIR}/tmp" >&2`,
		`  exit 1`,
		`}`,
		`export TMPDIR="${NOMAD_TASK_DIR}/tmp"`,
		`export TMP="${NOMAD_TASK_DIR}/tmp"`,
		`export TEMP="${NOMAD_TASK_DIR}/tmp"`,
		`# --- end abc task-tmp ---`,
	}
}

// prependTaskTmpIfNeeded inserts taskTmpPreambleLines after the shebang.
func prependTaskTmpIfNeeded(script string, spec *jobSpec) string {
	lines := taskTmpPreambleLines(spec)
	if len(lines) == 0 {
		return script
	}
	return insertLinesAfterShebang(script, lines)
}

// syncTaskTmpMeta records abc_task_tmp in Nomad meta when enabled.
func syncTaskTmpMeta(spec *jobSpec) {
	if spec == nil || !spec.TaskTmp {
		return
	}
	if spec.Meta == nil {
		spec.Meta = map[string]string{}
	}
	spec.Meta["abc_task_tmp"] = "true"
}
