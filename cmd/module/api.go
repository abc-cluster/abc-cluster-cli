package module

// BuildModuleHCL generates the Nomad HCL for an nf-core module run.
// spec.defaults() is called before generation.
func BuildModuleHCL(spec *RunSpec, nomadAddr, nomadToken string) string {
	spec.defaults()
	return generateModuleRunHCL(spec, nomadAddr, nomadToken, newRunUUID())
}
