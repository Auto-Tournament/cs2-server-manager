package license

// PublicKeys are the Ed25519 public keys this build trusts, by kid. The value
// is the raw 32-byte public key, base64url without padding (the JWK "x").
//
// Rotating the signing key adds an entry here; never remove an old one while
// keys signed with it are still in use. The website's
// src/lib/license/public-keys.json is the source of truth.
var PublicKeys = map[string]string{
	"tWl_YS3_AzLgqdkm": "YQMKIdrQtVz-QFV3Tw0AWa7RivnNtjK6PiJzskg8R0g",
}
