package config

// ContextCrypt holds local password-based crypt material for rclone and abc secrets (per context).
type ContextCrypt struct {
	Password string `yaml:"password,omitempty"`
	Salt     string `yaml:"salt,omitempty"`
}

// normalizeContextCrypt folds deprecated contexts.<name>.crypt_password / crypt_salt into
// contexts.<name>.crypt and clears the flat keys so the next save writes only the nested shape.
func normalizeContextCrypt(ctx *Context) {
	if ctx.Crypt.Password == "" && ctx.FlatCryptPassword != "" {
		ctx.Crypt.Password = ctx.FlatCryptPassword
	}
	if ctx.Crypt.Salt == "" && ctx.FlatCryptSalt != "" {
		ctx.Crypt.Salt = ctx.FlatCryptSalt
	}
	ctx.FlatCryptPassword = ""
	ctx.FlatCryptSalt = ""
}
