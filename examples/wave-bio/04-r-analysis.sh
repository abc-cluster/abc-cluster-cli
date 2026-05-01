#!/bin/bash
#ABC --name=wave-r-deseq2
#ABC --runtime=wave-exec
#ABC --from=environment.yml
#ABC --driver=docker
#ABC --cores=8
#ABC --mem=32G
#ABC --time=02:00:00
#ABC --task-tmp
#ABC --alloc_id
#
# Differential expression analysis with DESeq2 and edgeR.
#
# This script demonstrates a capability unique to wave-exec: R and Bioconductor
# packages (DESeq2, edgeR) are embedded in the Wave container via conda, so
# there is no per-job R package installation step.  The same container image is
# reused across all jobs that share this environment.yml.
#
# Input is a raw count matrix (rows = genes, columns = samples) and a sample
# sheet TSV with columns: sample, condition.  DESeq2 and edgeR are both run
# and their results written to outdir.
#
# Required meta keys:
#   counts     path to raw count matrix TSV (gene × sample, first col = gene_id)
#   samples    path to sample sheet TSV (columns: sample, condition)
#   outdir     destination directory for result tables + plots
#
# Optional:
#   pval_cutoff   adjusted p-value threshold for "significant" genes (default: 0.05)
#   lfc_cutoff    |log2FC| threshold for "significant" genes (default: 1.0)
#   ref_level     reference condition label in the sample sheet (default: first
#                 alphabetically among distinct condition values)
#
# Example:
#   abc job run 04-r-analysis.sh \
#     --meta counts=/data/counts/raw_counts.tsv \
#     --meta samples=/data/metadata/samples.tsv \
#     --meta outdir=/data/de \
#     --meta ref_level=control

set -euo pipefail

COUNTS="${NOMAD_META_COUNTS:?}"
SAMPLES="${NOMAD_META_SAMPLES:?}"
OUTDIR="${NOMAD_META_OUTDIR:?}"
THREADS=${NOMAD_CPU_CORES:-8}
PVAL="${NOMAD_META_PVAL_CUTOFF:-0.05}"
LFC="${NOMAD_META_LFC_CUTOFF:-1.0}"
REF_LEVEL="${NOMAD_META_REF_LEVEL:-}"

mkdir -p "${OUTDIR}"

echo "[R] DESeq2 + edgeR  counts=${COUNTS}  samples=${SAMPLES}"

Rscript - "${COUNTS}" "${SAMPLES}" "${OUTDIR}" "${PVAL}" "${LFC}" "${THREADS}" "${REF_LEVEL}" <<'RSCRIPT'
args       <- commandArgs(trailingOnly = TRUE)
counts_f   <- args[1]
samples_f  <- args[2]
outdir     <- args[3]
pval_cut   <- as.numeric(args[4])
lfc_cut    <- as.numeric(args[5])
threads    <- as.integer(args[6])
ref_level  <- args[7]   # may be empty string ""

suppressPackageStartupMessages({
  library(DESeq2)
  library(edgeR)
  library(ggplot2)
  library(dplyr)
  library(readr)
})

# ── Load data ────────────────────────────────────────────────────────────────
message("[R] reading counts: ", counts_f)
counts_raw <- read_tsv(counts_f, show_col_types = FALSE)
gene_ids   <- counts_raw[[1]]
count_mat  <- as.matrix(counts_raw[, -1])
rownames(count_mat) <- gene_ids

message("[R] reading sample sheet: ", samples_f)
coldata <- read_tsv(samples_f, show_col_types = FALSE) |>
  mutate(condition = factor(condition))

if (nchar(ref_level) > 0) {
  coldata$condition <- relevel(coldata$condition, ref = ref_level)
}

# Align column order
count_mat <- count_mat[, coldata$sample, drop = FALSE]

message("[R] ", nrow(count_mat), " genes × ", ncol(count_mat), " samples")

# ── DESeq2 ───────────────────────────────────────────────────────────────────
message("[DESeq2] running...")
dds <- DESeqDataSetFromMatrix(countData = count_mat,
                               colData   = coldata,
                               design    = ~ condition)
dds <- DESeq(dds, parallel = (threads > 1), BPPARAM = BiocParallel::MulticoreParam(threads))

res <- results(dds, alpha = pval_cut) |>
  as.data.frame() |>
  tibble::rownames_to_column("gene_id") |>
  arrange(padj)

write_tsv(res, file.path(outdir, "deseq2_results.tsv"))

sig_deseq2 <- sum(!is.na(res$padj) & res$padj < pval_cut & abs(res$log2FoldChange) >= lfc_cut)
message("[DESeq2] significant genes (padj<", pval_cut, ", |lfc|>=", lfc_cut, "): ", sig_deseq2)

# MA plot
pdf(file.path(outdir, "deseq2_ma.pdf"), width = 7, height = 5)
plotMA(dds, alpha = pval_cut, main = "DESeq2 MA plot")
dev.off()

# Volcano plot
p <- ggplot(res |> filter(!is.na(padj)),
            aes(x = log2FoldChange, y = -log10(padj),
                colour = padj < pval_cut & abs(log2FoldChange) >= lfc_cut)) +
  geom_point(alpha = 0.4, size = 0.8) +
  scale_colour_manual(values = c("grey60", "firebrick3"),
                      labels = c("not significant", "significant"),
                      name   = NULL) +
  labs(title = "DESeq2 volcano", x = "log2 fold change", y = "-log10(adj. p)") +
  theme_bw()
ggsave(file.path(outdir, "deseq2_volcano.pdf"), p, width = 7, height = 5)

# ── edgeR ────────────────────────────────────────────────────────────────────
message("[edgeR] running...")
group <- coldata$condition
dge   <- DGEList(counts = count_mat, group = group)
dge   <- filterByExpr(dge) |> (\(keep) dge[keep, , keep.lib.sizes = FALSE])()
dge   <- calcNormFactors(dge)

design_mat <- model.matrix(~ group)
dge        <- estimateDisp(dge, design_mat)
fit        <- glmQLFit(dge, design_mat)
qlf        <- glmQLFTest(fit, coef = ncol(design_mat))

edger_res <- topTags(qlf, n = Inf)$table |>
  tibble::rownames_to_column("gene_id") |>
  arrange(FDR)

write_tsv(edger_res, file.path(outdir, "edger_results.tsv"))

sig_edger <- sum(edger_res$FDR < pval_cut & abs(edger_res$logFC) >= lfc_cut)
message("[edgeR] significant genes (FDR<", pval_cut, ", |lfc|>=", lfc_cut, "): ", sig_edger)

message("[R] results written to: ", outdir)
RSCRIPT

echo "[R] done → ${OUTDIR}"
