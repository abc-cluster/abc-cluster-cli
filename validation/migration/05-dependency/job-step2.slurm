#!/bin/bash
# ── SLURM — Job Dependency: Step 2 (variant calling) ─────────────────────────
# Submit:  sbatch --dependency=afterok:$STEP1 job-step2.slurm

#SBATCH --job-name=wgs-step2-variantcall
#SBATCH --cpus-per-task=16
#SBATCH --mem=128G
#SBATCH --time=12:00:00
#SBATCH --output=step2-%j.out
#SBATCH --error=step2-%j.err

echo "[step2] GATK HaplotypeCaller on all BAMs"

BAMS=$(ls "$SLURM_SUBMIT_DIR"/bam/*.sorted.bam | tr '\n' ' ')

gatk HaplotypeCaller \
  --java-options "-Xmx120G" \
  -R "$SLURM_SUBMIT_DIR/reference/GRCh38.fa" \
  $(for b in $BAMS; do echo "-I $b"; done) \
  -O "$SLURM_SUBMIT_DIR/output/cohort.g.vcf.gz" \
  -ERC GVCF

echo "[step2] Done"
