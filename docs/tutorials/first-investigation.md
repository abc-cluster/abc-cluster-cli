# Your first investigation

This walks through the full headless flow that exercises every shipped
verb of the investigation primitive — modelled on Example 1 from the
`abc investigation visualize` brainstorm.

```bash
# Set up the project (top-level grouping for investigations)
abc project create "GenPath 2024 Mozambique" --slug=genpath-2024
abc project use genpath-2024

# Start the investigation
abc investigation create "Genome assembly for OxfordNanopore Q4 cohort"
# → I-cosmic-pelican-7 (auto-active)

# First hypothesis
abc investigation annotate cosmic-pelican-7 --tag=hypothesis \
  --note="viralrecon should work for nanopore with appropriate flags"

# First attempt; auto-attached to the active investigation
abc pipeline run nf-core/viralrecon@2.6.0 --input=samplesheet.csv

# Document the failure
abc investigation annotate cosmic-pelican-7 --tag=issue \
  --note="viralrecon doesn't handle long reads correctly"

# Branch to try a nanopore-native approach
abc investigation branch cosmic-pelican-7 "nanopore-specific approach"
abc investigation use nanopore-specific

abc pipeline run artic-network/fieldbioinformatics@1.4 --primers=artic-v3
abc investigation annotate nanopore-specific --tag=decision \
  --note="going with artic for nanopore data"

# Promote the working branch back into the parent
abc investigation merge nanopore-specific --into cosmic-pelican-7 \
  --note="artic adopted as the nanopore path"

# Render the four views
abc investigation visualize cosmic-pelican-7
abc investigation visualize cosmic-pelican-7 --type=timeline
abc investigation visualize cosmic-pelican-7 --type=flow
abc investigation visualize cosmic-pelican-7 --type=lineage

# Export to a portable bundle for review
abc investigation export cosmic-pelican-7 --format=markdown \
  --output=./report.md
```

Inspect the underlying state at any time:

```bash
abc cache status
abc investigation show cosmic-pelican-7 --output=json | jq .
abc investigation tree
```
