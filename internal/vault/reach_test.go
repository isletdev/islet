package vault

import (
	"strings"
	"testing"
)

// The reference has to be recognised in the shapes people actually write, not
// only in a bare value.
//
// The vault page has always told people they could write @vault:NAME in an
// app's environment or a cron command. Only the first of those ever resolved
// it, so anybody who followed the second got the literal string where a
// password should have been — which fails in whatever way that particular tool
// fails when its password is the eleven characters "@vault:NAME".
func TestAReferenceIsFoundWhereItIsActuallyWritten(t *testing.T) {
	for _, tc := range []struct {
		what, in string
		want     []string
	}{
		{"an env line", "DATABASE_PASSWORD=@vault:SHOP_DB", []string{"SHOP_DB"}},
		{"a cron command", "pg_dump -h db -U shop --password=@vault:SHOP_DB > /backups/shop.sql", []string{"SHOP_DB"}},
		{"a catalog field", "@vault:KEYCLOAK_ADMIN", []string{"KEYCLOAK_ADMIN"}},
		{"inside a URL", "postgres://shop:@vault:SHOP_DB@db:5432/shop", []string{"SHOP_DB"}},
		{"two in one line", "A=@vault:ONE B=@vault:TWO", []string{"ONE", "TWO"}},
		{"none at all", "DATABASE_PASSWORD=hunter2", nil},
	} {
		got := Refs(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("%s: found %v, want %v", tc.what, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: found %v, want %v", tc.what, got, tc.want)
				break
			}
		}
	}
}

// A name that is not in the vault is left exactly as written. It must not
// become an empty string: an empty password is a database that accepts the
// connection, and a reference that survives is a tool that refuses it loudly.
func TestAMissingSecretIsLeftAlone(t *testing.T) {
	const in = "PASSWORD=@vault:NOT_THERE"
	if refs := Refs(in); len(refs) != 1 || refs[0] != "NOT_THERE" {
		t.Fatalf("the reference was not even recognised: %v", refs)
	}
	if strings.Contains(in, "=\"\"") {
		t.Fatal("this test is wrong")
	}
}
