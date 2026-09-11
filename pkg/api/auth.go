package api

// SetupStatus is returned by GET /api/v1/setup.
type SetupStatus struct {
	NeedsSetup   bool   `json:"needsSetup"`
	CookieDomain string `json:"cookieDomain,omitempty"`
}

// SetupRequest creates the first admin. Token comes from the installer's
// one-time link.
type SetupRequest struct {
	Token    string `json:"token"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginRequest is the first login step.
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResponse tells the client whether a second factor is still needed.
type LoginResponse struct {
	MFARequired bool  `json:"mfaRequired"`
	User        *User `json:"user,omitempty"`
}

// CodeRequest carries a TOTP or recovery code.
type CodeRequest struct {
	Code string `json:"code"`
}

// PasswordChangeRequest changes the caller's password.
type PasswordChangeRequest struct {
	Current string `json:"current"`
	New     string `json:"new"`
}

// User is the caller's account as the UI sees it.
type User struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Role        string `json:"role"`
	TOTPEnabled bool   `json:"totpEnabled"`
	CreatedAt   string `json:"createdAt"`
	LastLoginAt string `json:"lastLoginAt,omitempty"`
}

// Me is returned by GET /api/v1/auth/me.
type Me struct {
	User              User   `json:"user"`
	SessionID         string `json:"sessionId"`
	RecoveryCodesLeft int    `json:"recoveryCodesLeft"`
}

// TOTPSetup is returned when starting two-factor setup.
type TOTPSetup struct {
	Secret     string `json:"secret"`
	OTPAuthURL string `json:"otpauthUrl"`
}

// RecoveryCodes are shown once after enabling two-factor.
type RecoveryCodes struct {
	Codes []string `json:"codes"`
}

// Session is one logged-in browser.
type Session struct {
	ID         string `json:"id"`
	Current    bool   `json:"current"`
	IP         string `json:"ip"`
	UserAgent  string `json:"userAgent"`
	CreatedAt  string `json:"createdAt"`
	LastSeenAt string `json:"lastSeenAt"`
	ExpiresAt  string `json:"expiresAt"`
}
