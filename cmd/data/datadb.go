package data

// datadb.go — registry of scientific data repositories that abc data download
// can acquire from. Each entry describes a database's accession format,
// authentication requirements, supported output formats, and the backend
// implementation used to fetch data.
//
// Design:
//   - Auto-detection: every accession pattern is a regexp; the first pattern
//     match across all databases identifies the source without --from.
//   - Explicit override: --from <id|alias> always wins over auto-detection.
//   - Backends: "fetchngs" (nf-core/fetchngs Nextflow pipeline, handles SRA/
//     ENA/DDBJ/GEO), "datasets" (NCBI datasets CLI for reference genomes),
//     "direct" (custom implementation), "pending" (not yet implemented).
//
// Adding a new database: append a Database{} entry below. No other changes
// are needed — the auto-detection, --from flag, and help text are all derived
// from this table at runtime.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// AuthType describes the authentication model for a database.
type AuthType string

const (
	AuthOpen        AuthType = "open"        // public, no credentials needed
	AuthToken       AuthType = "token"       // API token / data access agreement token
	AuthApplication AuthType = "application" // requires a formal application (DAC approval)
)

// Backend names the implementation used to fetch data from a database.
type Backend string

const (
	BackendFetchngs Backend = "fetchngs" // nf-core/fetchngs Nextflow pipeline
	BackendDatasets Backend = "datasets" // NCBI datasets CLI
	BackendDirect   Backend = "direct"   // custom implementation in abc-cluster-cli
	BackendPending  Backend = "pending"  // not yet implemented; displays --from note
)

// Database describes one scientific data repository.
type Database struct {
	ID       string   // canonical key for --from (lowercase, hyphen-separated)
	Name     string   // human-readable display name
	Aliases  []string // alternative --from values accepted
	Domain   string   // scientific domain: sequence, expression, reference, pathogen, proteomics, metabolomics, structure
	Auth     AuthType // authentication model
	Formats  []string // supported output formats (first is default)
	Backend  Backend  // fetch implementation
	Note     string   // shown in help and "pending" error messages
	patterns []*regexp.Regexp
}

// accessionPatterns lists the raw regexp strings for each database.
// Kept separate from the Database struct to keep the table readable.
var accessionPatterns = map[string][]string{
	// ── Sequence data (SRA/ENA/DDBJ insdc) ──────────────────────────────
	"sra": {
		`^SRR\d+$`,    // run
		`^SRX\d+$`,    // experiment
		`^SRS\d+$`,    // sample
		`^SRA\d+$`,    // submission
		`^SRP\d+$`,    // study
		`^PRJNA\d+$`,  // bioproject (NCBI)
		`^SAMN\d+$`,   // biosample
	},
	"ena": {
		`^ERR\d+$`,    // run
		`^ERX\d+$`,    // experiment
		`^ERS\d+$`,    // sample
		`^ERA\d+$`,    // submission
		`^ERP\d+$`,    // study
		`^PRJEB\d+$`,  // bioproject (EBI)
		`^SAMEA\d+$`,  // biosample
	},
	"ddbj": {
		`^DRR\d+$`,    // run
		`^DRX\d+$`,    // experiment
		`^DRS\d+$`,    // sample
		`^DRA\d+$`,    // submission
		`^DRP\d+$`,    // study
		`^PRJDB\d+$`,  // bioproject (DDBJ)
		`^SAMD\d+$`,   // biosample
	},
	// ── Controlled-access sequence ───────────────────────────────────────
	"ega": {
		`^EGAD\d{11}$`, // dataset
		`^EGAS\d{11}$`, // study
		`^EGAN\d{11}$`, // analysis
		`^EGAF\d{11}$`, // file
		`^EGAX\d{11}$`, // experiment
	},
	"dbgap": {
		`^phs\d+\.v\d+\.p\d+$`, // full versioned accession
		`^phs\d+$`,              // bare study accession
		`^phd\d+$`,              // document
	},
	// ── Gene expression ──────────────────────────────────────────────────
	"geo": {
		`^GSE\d+$`, // series (study)
		`^GSM\d+$`, // sample
		`^GPL\d+$`, // platform
		`^GDS\d+$`, // dataset
	},
	"arrayexpress": {
		`^E-[A-Z]{4}-\d+$`, // e.g. E-MTAB-1234, E-GEOD-5678
	},
	// ── Reference genomes / sequences ────────────────────────────────────
	"refseq": {
		`^GCF_\d{9}\.\d+$`, // RefSeq assembly (e.g. GCF_000001405.40)
		`^GCA_\d{9}\.\d+$`, // GenBank assembly (e.g. GCA_000001405.29)
		`^NC_\d{6}$`,       // RefSeq chromosome/contig (e.g. NC_000001)
		`^NM_\d{6}$`,       // RefSeq mRNA
		`^NP_\d{6}$`,       // RefSeq protein
		`^NR_\d{6}$`,       // RefSeq non-coding RNA
		`^NG_\d{6}$`,       // RefSeq genomic region
	},
	// ── Structure ────────────────────────────────────────────────────────
	"pdb": {
		`^[0-9][A-Za-z0-9]{3}$`,             // classic 4-char PDB ID (e.g. 1ABC)
		`^pdb_\d{5}[A-Za-z0-9]{3}$`,         // extended PDB ID (e.g. pdb_00001abc)
	},
	// ── Protein sequence ─────────────────────────────────────────────────
	"uniprot": {
		// Swiss-Prot / TrEMBL accession (6 or 10 chars)
		`^[OPQ][0-9][A-Z0-9]{3}[0-9]$`,
		`^[A-NR-Z][0-9]([A-Z][A-Z0-9]{2}[0-9]){1,2}$`,
	},
	// ── Proteomics ───────────────────────────────────────────────────────
	"pride": {
		`^PXD\d{6}$`, // ProteomeXchange / PRIDE dataset
	},
	// ── Metabolomics ─────────────────────────────────────────────────────
	"metabolights": {
		`^MTBLS\d+$`, // MetaboLights study
	},
	"metabolomicsworkbench": {
		`^ST\d{6}$`, // Metabolomics Workbench study
	},
	// ── Pathogen / surveillance ───────────────────────────────────────────
	"gisaid": {
		`^EPI_ISL_\d+$`, // GISAID SARS-CoV-2 / influenza EPI accession
	},
	"bvbrc": {
		// BV-BRC (Bacterial and Viral Bioinformatics Resource Center, fka PATRIC)
		// genome IDs are <taxon_id>.<genome_number>, e.g. 1763.12345
		`^\d+\.\d+$`,
	},
}

// databases is the ordered, authoritative registry of supported repositories.
// Order matters: entries are shown in this order in help text and searched in
// this order for auto-detection (earlier entries win on pattern overlap).
var databases = []Database{
	// ── Sequence data ────────────────────────────────────────────────────
	{
		ID:      "sra",
		Name:    "NCBI SRA",
		Aliases: []string{"ncbi-sra", "ncbi", "sequence-read-archive"},
		Domain:  "sequence",
		Auth:    AuthOpen,
		Formats: []string{"fastq", "bam", "sra"},
		Backend: BackendFetchngs,
		Note:    "Sequence Read Archive — run (SRR), experiment (SRX), study (SRP), BioProject (PRJNA), BioSample (SAMN).",
	},
	{
		ID:      "ena",
		Name:    "EBI ENA",
		Aliases: []string{"ebi-ena", "european-nucleotide-archive"},
		Domain:  "sequence",
		Auth:    AuthOpen,
		Formats: []string{"fastq", "bam"},
		Backend: BackendFetchngs,
		Note:    "European Nucleotide Archive — run (ERR), experiment (ERX), study (ERP), BioProject (PRJEB), BioSample (SAMEA).",
	},
	{
		ID:      "ddbj",
		Name:    "DDBJ",
		Aliases: []string{"dna-data-bank-japan"},
		Domain:  "sequence",
		Auth:    AuthOpen,
		Formats: []string{"fastq", "bam", "sra"},
		Backend: BackendFetchngs,
		Note:    "DNA Data Bank of Japan — run (DRR), experiment (DRX), study (DRP), BioProject (PRJDB), BioSample (SAMD).",
	},
	{
		ID:      "ega",
		Name:    "EGA",
		Aliases: []string{"european-genome-phenome-archive"},
		Domain:  "sequence",
		Auth:    AuthApplication,
		Formats: []string{"fastq", "bam", "cram", "vcf"},
		Backend: BackendPending,
		Note:    "European Genome-phenome Archive — controlled access; requires Data Access Agreement (DAC) approval. Provide --credentials <path-to-ega-credentials-file>.",
	},
	{
		ID:      "dbgap",
		Name:    "dbGaP",
		Aliases: []string{"ncbi-dbgap", "database-of-genotypes-and-phenotypes"},
		Domain:  "sequence",
		Auth:    AuthApplication,
		Formats: []string{"sra", "fastq", "vcf"},
		Backend: BackendPending,
		Note:    "NCBI Database of Genotypes and Phenotypes — controlled access; requires dbGaP access approval and an SRA toolkit configuration.",
	},
	// ── Gene expression ──────────────────────────────────────────────────
	{
		ID:      "geo",
		Name:    "NCBI GEO",
		Aliases: []string{"ncbi-geo", "gene-expression-omnibus"},
		Domain:  "expression",
		Auth:    AuthOpen,
		Formats: []string{"fastq", "txt", "csv"},
		Backend: BackendFetchngs,
		Note:    "Gene Expression Omnibus — series (GSE), sample (GSM), platform (GPL).",
	},
	{
		ID:      "arrayexpress",
		Name:    "ArrayExpress",
		Aliases: []string{"ebi-arrayexpress", "biostudies"},
		Domain:  "expression",
		Auth:    AuthOpen,
		Formats: []string{"fastq", "txt"},
		Backend: BackendPending,
		Note:    "EBI ArrayExpress / BioStudies — accession format E-XXXX-NNNN (e.g. E-MTAB-1234).",
	},
	// ── Reference genomes / sequences ────────────────────────────────────
	{
		ID:      "refseq",
		Name:    "NCBI RefSeq",
		Aliases: []string{"ncbi-refseq", "ncbi-assembly", "ncbi-datasets"},
		Domain:  "reference",
		Auth:    AuthOpen,
		Formats: []string{"fasta", "gff", "gbk", "vcf"},
		Backend: BackendDatasets,
		Note:    "NCBI Reference Sequences and Assembly — GCF_/GCA_ assembly accessions, NC_/NM_/NP_ sequence accessions. Uses the NCBI datasets CLI.",
	},
	{
		ID:      "ensembl",
		Name:    "Ensembl",
		Aliases: []string{"ensembl-release"},
		Domain:  "reference",
		Auth:    AuthOpen,
		Formats: []string{"fasta", "gtf", "gff3"},
		Backend: BackendPending,
		Note:    "Ensembl release downloads — specify organism and release number (e.g. --accession homo_sapiens --revision 110). Downloads reference genome + annotation via Ensembl FTP.",
	},
	{
		ID:      "ucsc",
		Name:    "UCSC",
		Aliases: []string{"ucsc-genome-browser"},
		Domain:  "reference",
		Auth:    AuthOpen,
		Formats: []string{"fasta", "2bit", "bed", "bigwig"},
		Backend: BackendPending,
		Note:    "UCSC Genome Browser downloads — specify assembly name (hg38, mm10, sacCer3, etc.).",
	},
	// ── Annotation / taxonomic databases ─────────────────────────────────
	{
		ID:      "silva",
		Name:    "SILVA",
		Aliases: []string{"silva-rrna"},
		Domain:  "reference",
		Auth:    AuthOpen,
		Formats: []string{"fasta", "taxonomy"},
		Backend: BackendPending,
		Note:    "SILVA ribosomal RNA database — 16S, 18S, 23S, 28S; specify release (e.g. --revision 138.1) and subunit (--format SSU or LSU).",
	},
	{
		ID:      "gtdb",
		Name:    "GTDB",
		Aliases: []string{"genome-taxonomy-database"},
		Domain:  "reference",
		Auth:    AuthOpen,
		Formats: []string{"fasta", "taxonomy", "tree"},
		Backend: BackendPending,
		Note:    "Genome Taxonomy Database — prokaryotic taxonomy and representative genomes. Specify release (e.g. --revision r214).",
	},
	// ── Structure ────────────────────────────────────────────────────────
	{
		ID:      "pdb",
		Name:    "PDB",
		Aliases: []string{"protein-data-bank", "rcsb"},
		Domain:  "structure",
		Auth:    AuthOpen,
		Formats: []string{"pdb", "mmcif", "pdbml", "fasta"},
		Backend: BackendPending,
		Note:    "RCSB Protein Data Bank — 4-character structure IDs (e.g. 1ABC) or extended IDs.",
	},
	// ── Protein sequence ─────────────────────────────────────────────────
	{
		ID:      "uniprot",
		Name:    "UniProt",
		Aliases: []string{"swiss-prot", "trembl", "uniref"},
		Domain:  "structure",
		Auth:    AuthOpen,
		Formats: []string{"fasta", "txt", "json", "xml"},
		Backend: BackendPending,
		Note:    "UniProt protein sequence and annotation — Swiss-Prot/TrEMBL accessions (e.g. P12345, A0A000).",
	},
	// ── Proteomics ───────────────────────────────────────────────────────
	{
		ID:      "pride",
		Name:    "PRIDE / ProteomeXchange",
		Aliases: []string{"proteomexchange", "ebi-pride"},
		Domain:  "proteomics",
		Auth:    AuthOpen,
		Formats: []string{"raw", "mzml", "mzxml", "mztab"},
		Backend: BackendPending,
		Note:    "EMBL-EBI PRIDE proteomics archive — ProteomeXchange accessions (PXD...) via Aspera or FTP.",
	},
	// ── Metabolomics ─────────────────────────────────────────────────────
	{
		ID:      "metabolights",
		Name:    "MetaboLights",
		Aliases: []string{"ebi-metabolights"},
		Domain:  "metabolomics",
		Auth:    AuthOpen,
		Formats: []string{"mzml", "nmrml", "txt", "csv"},
		Backend: BackendPending,
		Note:    "EBI MetaboLights metabolomics study archive — MTBLS accessions.",
	},
	{
		ID:      "metabolomicsworkbench",
		Name:    "Metabolomics Workbench",
		Aliases: []string{"mw", "sdsc-mw"},
		Domain:  "metabolomics",
		Auth:    AuthOpen,
		Formats: []string{"mzml", "txt", "csv"},
		Backend: BackendPending,
		Note:    "SDSC Metabolomics Workbench — ST accessions (e.g. ST000001).",
	},
	// ── Pathogen / surveillance ───────────────────────────────────────────
	{
		ID:      "gisaid",
		Name:    "GISAID",
		Aliases: []string{"gisaid-epi"},
		Domain:  "pathogen",
		Auth:    AuthToken,
		Formats: []string{"fasta", "vcf", "metadata"},
		Backend: BackendPending,
		Note:    "GISAID SARS-CoV-2 and influenza surveillance data — EPI_ISL_ accessions. Requires a GISAID account and credentials file (--credentials path/to/gisaid.json).",
	},
	{
		ID:      "bvbrc",
		Name:    "BV-BRC",
		Aliases: []string{"patric", "bv-brc"},
		Domain:  "pathogen",
		Auth:    AuthOpen,
		Formats: []string{"fasta", "gff", "tsv"},
		Backend: BackendPending,
		Note:    "Bacterial and Viral Bioinformatics Resource Center (formerly PATRIC) — genome IDs in <taxon_id>.<number> format.",
	},
}

// compiledDatabases is the registry with patterns compiled to *regexp.Regexp.
// Built once at init time.
var compiledDatabases []Database

func init() {
	for _, db := range databases {
		patterns, ok := accessionPatterns[db.ID]
		if ok {
			for _, p := range patterns {
				db.patterns = append(db.patterns, regexp.MustCompile(p))
			}
		}
		compiledDatabases = append(compiledDatabases, db)
	}
}

// DetectDatabase returns the database whose accession patterns match acc,
// or nil if no match is found. Case-insensitive on acc.
func DetectDatabase(acc string) *Database {
	upper := strings.ToUpper(strings.TrimSpace(acc))
	for i, db := range compiledDatabases {
		for _, pat := range db.patterns {
			if pat.MatchString(upper) || pat.MatchString(acc) {
				return &compiledDatabases[i]
			}
		}
	}
	return nil
}

// LookupDatabase returns the Database for a given ID or alias (case-insensitive).
func LookupDatabase(from string) *Database {
	lower := strings.ToLower(strings.TrimSpace(from))
	for i, db := range compiledDatabases {
		if strings.ToLower(db.ID) == lower {
			return &compiledDatabases[i]
		}
		for _, a := range db.Aliases {
			if strings.ToLower(a) == lower {
				return &compiledDatabases[i]
			}
		}
	}
	return nil
}

// DatabasesByDomain returns all databases for a given domain, sorted by name.
func DatabasesByDomain(domain string) []Database {
	var out []Database
	for _, db := range compiledDatabases {
		if strings.EqualFold(db.Domain, domain) {
			out = append(out, db)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// DatabaseSummaryTable returns a formatted table of all databases for use in
// help text. Each row: ID, domain, auth, formats, status.
func DatabaseSummaryTable() string {
	var sb strings.Builder
	sb.WriteString("  Database             Domain        Auth          Status\n")
	sb.WriteString("  ─────────────────    ──────────    ──────────    ──────────\n")
	for _, db := range compiledDatabases {
		status := "available"
		if db.Backend == BackendPending {
			status = "coming soon"
		}
		sb.WriteString(fmt.Sprintf("  %-20s %-13s %-13s %s\n",
			db.ID, db.Domain, string(db.Auth), status,
		))
	}
	return sb.String()
}

// SupportedDatabaseIDs returns a flat list of all database IDs and aliases,
// for shell completion.
func SupportedDatabaseIDs() []string {
	var out []string
	for _, db := range compiledDatabases {
		out = append(out, db.ID)
		out = append(out, db.Aliases...)
	}
	sort.Strings(out)
	return out
}

// IsFetchngsSupported reports whether a database uses the fetchngs backend.
func (db *Database) IsFetchngsSupported() bool { return db.Backend == BackendFetchngs }

// IsPending reports whether the database backend is not yet implemented.
func (db *Database) IsPending() bool { return db.Backend == BackendPending }

// DefaultFormat returns the first listed format (the recommended default).
func (db *Database) DefaultFormat() string {
	if len(db.Formats) == 0 {
		return ""
	}
	return db.Formats[0]
}

// FormatList returns the supported formats as a comma-separated string.
func (db *Database) FormatList() string { return strings.Join(db.Formats, ", ") }
