package update

// PublicKey is the Ed25519 release signing key, base64. The private half is
// held by the maintainer outside the repository and in CI as ISLET_SIGNING_KEY.
// Rotating it means shipping one release signed by the old key that carries
// the new key, then switching.
var PublicKey = "LQocWNPSIb1cTZCmPu6Qgx8pXqqgEBA4+f1+7a1vIG8="
