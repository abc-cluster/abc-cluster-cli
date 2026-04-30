#!/bin/bash
#ABC --name=pixi-diag

echo "=== NOMAD_TASK_DIR=${NOMAD_TASK_DIR}"
echo "=== listing task dir:"
ls -la "${NOMAD_TASK_DIR}/" 2>&1 || echo "(ls failed)"
echo "=== checking pixi binary:"
ls -la "${NOMAD_TASK_DIR}/pixi" 2>&1 || echo "(no pixi at NOMAD_TASK_DIR/pixi)"
echo "=== PATH before wrapper: ${PATH}"
