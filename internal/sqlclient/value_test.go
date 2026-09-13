package sqlclient

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

// TestEncodeRepresentations pins down every representation decided on Value.
// If one of these changes, the frontend grid changes with it, so changing one
// deliberately means changing this table and the comment it enforces.
func TestEncodeRepresentations(t *testing.T) {
	utc := time.Date(2026, 9, 13, 14, 5, 6, 123456000, time.UTC)

	cases := []struct {
		name  string
		in    any
		class Class
		want  string // the JSON the client receives
	}{
		{"null is null", nil, ClassString, `null`},
		{"empty string is not null", "", ClassString, `""`},
		{"bool", true, ClassBool, `true`},
		{"postgres bool as text", "t", ClassBool, `true`},

		{"int4 is a number", int32(42), ClassNumber, `42`},
		{"int8 in range is still a string", int64(42), ClassDecimal, `"42"`},
		{"int beyond 2^53 never becomes a number", int64(9007199254740993), ClassNumber, `"9007199254740993"`},
		{"negative beyond 2^53", int64(-9007199254740993), ClassNumber, `"-9007199254740993"`},
		{"numeric keeps every digit", "12345678901234567890.0001", ClassDecimal, `"12345678901234567890.0001"`},

		{"float", 1.5, ClassNumber, `1.5`},
		{"NaN has no JSON form", math.NaN(), ClassNumber, `"NaN"`},
		{"positive infinity", math.Inf(1), ClassNumber, `"Infinity"`},
		{"negative infinity", math.Inf(-1), ClassNumber, `"-Infinity"`},

		{"timestamptz is UTC RFC 3339", utc, ClassDateTime, `"2026-09-13T14:05:06.123456Z"`},
		{"a date is a day, with no midnight glued on", utc, ClassDate, `"2026-09-13"`},
		{"a time is a clock time, with no day", utc, ClassTime, `"14:05:06.123456"`},

		{"jsonb is embedded, not stringified", `{"a":[1,2]}`, ClassJSON, `{"a":[1,2]}`},
		{"a json column holding invalid json shows the text", `not json`, ClassJSON, `"not json"`},

		{"bytea is base64", []byte{0x00, 0x01, 0xff}, ClassBinary, `"AAH/"`},
		{"mysql text arrives as bytes", []byte("hello"), ClassString, `"hello"`},

		{"uuid", [16]byte{0x8f, 0x14, 0x25, 0xa2, 0x2b, 0x4c, 0x4d, 0x1e, 0x9a, 0x33, 0x00, 0x11, 0x22, 0x33, 0x44, 0x55}, ClassUUID,
			`"8f1425a2-2b4c-4d1e-9a33-001122334455"`},

		{"array of the element representation", []any{int32(1), nil, int32(3)}, ClassArray, `[1,null,3]`},
		{"array of text", []string{"a", "b"}, ClassArray, `["a","b"]`},

		{"an interval keeps the engine's words", "1 mon 2 days 03:04:05", ClassInterval, `"1 mon 2 days 03:04:05"`},
		{"unknown types are the driver's text", "[1,10)", ClassUnknown, `"[1,10)"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(encode(tc.in, tc.class))
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(b) != tc.want {
				t.Errorf("encode(%#v, %s) = %s, want %s", tc.in, tc.class, b, tc.want)
			}
		})
	}
}

// TestNullIsNotEmpty is the one the console could never get right, so it gets
// its own test rather than hiding in the table above.
func TestNullIsNotEmpty(t *testing.T) {
	if encode(nil, ClassString) != nil {
		t.Fatal("NULL must encode as JSON null")
	}
	if got := encode("", ClassString); got != "" {
		t.Fatalf("empty string encoded as %#v, want an empty string", got)
	}
}

func TestClassFor(t *testing.T) {
	cases := []struct {
		engine, typ string
		want        Class
	}{
		{"postgres", "int4", ClassNumber},
		{"postgres", "int8", ClassDecimal},
		{"postgres", "numeric", ClassDecimal},
		{"postgres", "jsonb", ClassJSON},
		{"postgres", "timestamptz", ClassDateTime},
		{"postgres", "date", ClassDate},
		{"postgres", "time", ClassTime},
		{"postgres", "interval", ClassInterval},
		{"postgres", "bytea", ClassBinary},
		{"postgres", "_text", ClassArray},
		{"postgres", "int4range", ClassUnknown},
		{"mysql", "BIGINT", ClassDecimal},
		{"mysql", "TINYINT", ClassNumber},
		{"mysql", "JSON", ClassJSON},
		{"mysql", "LONGBLOB", ClassBinary},
		{"mariadb", "DATETIME", ClassDateTime},
		{"mysql", "POINT", ClassUnknown},
	}
	for _, tc := range cases {
		if got := classFor(tc.engine, tc.typ); got != tc.want {
			t.Errorf("classFor(%s, %s) = %s, want %s", tc.engine, tc.typ, got, tc.want)
		}
	}
}
