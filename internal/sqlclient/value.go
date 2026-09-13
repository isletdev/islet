package sqlclient

import (
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Class is how a column should be read and rendered. The engine's own type
// name travels alongside it and is what the user sees; the class is what the
// grid switches on, so adding a type never means teaching the frontend a new
// name.
type Class string

const (
	ClassString   Class = "string"
	ClassNumber   Class = "number"   // safe to hold in a JavaScript number
	ClassDecimal  Class = "decimal"  // exact, carried as a string
	ClassBool     Class = "bool"     //
	ClassDate     Class = "date"     // a calendar day, with no time on it
	ClassTime     Class = "time"     // a clock time, with no day on it
	ClassDateTime Class = "datetime" //
	ClassInterval Class = "interval" //
	ClassJSON     Class = "json"     // embedded, not stringified: the grid collapses it
	ClassArray    Class = "array"    //
	ClassBinary   Class = "binary"   // base64, with the byte length reported separately
	ClassUUID     Class = "uuid"     //
	ClassUnknown  Class = "unknown"  // rendered as text, whatever the driver gave us
)

// Column describes one column of a result.
type Column struct {
	Name  string `json:"name"`
	Type  string `json:"type"`  // the engine's own name for it: int4, timestamptz, varchar
	Class Class  `json:"class"` // how to render it
	// Table and Key are filled only when the engine reports a single source
	// table for the column. Inline editing (7.15) refuses without them, and
	// says so, rather than guessing which row it would be updating.
	Table string `json:"table,omitempty"`
	Key   bool   `json:"key,omitempty"`
}

// Value is one cell, already in its JSON representation.
//
// The representations are decided here and nowhere else. Every one of them
// exists because the obvious alternative loses information:
//
//   - SQL NULL is JSON null. An empty string is "". A client that cannot tell
//     them apart is the reason this package does not shell out to psql.
//   - Small integers, floats and int2/int4 are JSON numbers. int8, bigint,
//     numeric and decimal are JSON strings, always, per column and never per
//     row: a JavaScript number silently rounds past 2^53, and a column whose
//     type changes shape halfway down is worse than one that is consistently
//     a string.
//   - NaN and the infinities are the strings "NaN", "Infinity", "-Infinity",
//     because JSON has no way to write them.
//   - Dates are "2006-01-02" and times are "15:04:05.999999999", each with
//     its own class, because a date rendered as a timestamp is a date with a
//     midnight glued to it that the user then has to ignore. Timestamps are
//     RFC 3339 in UTC.
//   - Intervals keep the engine's own text form. It is what the user typed
//     and what they would type again.
//   - json and jsonb are embedded as parsed JSON, not as a string, so the
//     grid can collapse them. Invalid JSON falls back to the raw text.
//   - Arrays are JSON arrays of the element representation, recursively.
//   - bytea, blob and bit are base64. The column class says binary so the
//     grid shows a size and an expander rather than a wall of characters.
//   - Anything else is the driver's text form, with the class unknown. It is
//     honest about not knowing rather than pretending it is a string.
type Value = any

// encode converts a driver value into its wire representation. class comes
// from the column and decides the cases the Go type cannot: an int64 that must
// stay exact, a []byte that is text rather than bytes.
func encode(v any, class Class) Value {
	if v == nil {
		return nil
	}
	switch x := v.(type) {
	case bool:
		return x

	case int8:
		return int64(x)
	case int16:
		return int64(x)
	case int32:
		return int64(x)
	case int:
		return encodeInt(int64(x), class)
	case int64:
		return encodeInt(x, class)
	case uint8:
		return int64(x)
	case uint16:
		return int64(x)
	case uint32:
		return int64(x)
	case uint, uint64:
		u, _ := toUint64(x)
		if u > 1<<53 {
			return strconv.FormatUint(u, 10)
		}
		return int64(u)

	case float32:
		return encodeFloat(float64(x))
	case float64:
		return encodeFloat(x)

	case string:
		return encodeText(x, class)

	case []byte:
		// The MySQL driver hands back []byte for text as well as for blobs,
		// so the column class is the only thing that can tell them apart.
		if class == ClassBinary {
			return base64.StdEncoding.EncodeToString(x)
		}
		return encodeText(string(x), class)

	case time.Time:
		return encodeTime(x, class)

	case [16]byte:
		return formatUUID(x)

	case map[string]any, []any:
		return v // already JSON-shaped: jsonb from pgx arrives like this

	case json.RawMessage:
		return encodeText(string(x), ClassJSON)

	case driver.Valuer:
		// pgx decodes numeric, interval, bit and friends into its own structs.
		// They all satisfy driver.Valuer, which hands back one of the six
		// types above, so asking is better than importing pgtype here and
		// better than fmt.Sprint, which renders a numeric as "{725 -2 false
		// finite true}".
		dv, err := x.Value()
		if err != nil {
			break
		}
		if dv == nil {
			return nil
		}
		return encode(dv, class)

	case fmt.Stringer:
		return encodeText(x.String(), class)
	}

	// Arrays and anything else the driver decoded into a Go slice.
	if arr, ok := asSlice(v); ok {
		out := make([]Value, len(arr))
		for i, e := range arr {
			out[i] = encode(e, elementClass(class))
		}
		return out
	}
	return fmt.Sprint(v)
}

func encodeInt(n int64, class Class) Value {
	// Exactness beats convenience: a bigint id that arrives rounded is a bug
	// report nobody can reproduce.
	if class == ClassDecimal || n > 1<<53 || n < -(1<<53) {
		return strconv.FormatInt(n, 10)
	}
	return n
}

func encodeFloat(f float64) Value {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	return f
}

func encodeText(s string, class Class) Value {
	switch class {
	case ClassJSON:
		var out any
		if err := json.Unmarshal([]byte(s), &out); err == nil {
			return out
		}
		return s // the column claims json and the value is not: show the text
	case ClassNumber:
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return encodeInt(n, class)
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return encodeFloat(f)
		}
		return s
	case ClassBool:
		switch strings.ToLower(s) {
		case "t", "true", "1":
			return true
		case "f", "false", "0":
			return false
		}
		return s
	}
	return s
}

func encodeTime(t time.Time, class Class) Value {
	switch class {
	case ClassDate:
		return t.Format("2006-01-02")
	case ClassTime:
		return t.Format("15:04:05.999999999")
	case ClassDateTime:
		return t.UTC().Format(time.RFC3339Nano)
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func formatUUID(b [16]byte) string {
	const hex = "0123456789abcdef"
	out := make([]byte, 0, 36)
	for i, c := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out = append(out, '-')
		}
		out = append(out, hex[c>>4], hex[c&0x0f])
	}
	return string(out)
}

func toUint64(v any) (uint64, bool) {
	switch x := v.(type) {
	case uint:
		return uint64(x), true
	case uint64:
		return x, true
	}
	return 0, false
}

func asSlice(v any) ([]any, bool) {
	switch x := v.(type) {
	case []any:
		return x, true
	case []string:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = e
		}
		return out, true
	case []int64:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = e
		}
		return out, true
	case []int32:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = e
		}
		return out, true
	case []float64:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = e
		}
		return out, true
	}
	return nil, false
}

// elementClass is the class of an array's elements. The array class itself
// says nothing about what is inside, so the elements are classified by their
// Go type on the way through.
func elementClass(c Class) Class {
	if c == ClassArray {
		return ClassUnknown
	}
	return c
}
