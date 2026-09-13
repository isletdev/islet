package sqlclient

import "strings"

// classFor maps an engine's own type name onto the class the grid renders by.
// Unrecognised types are ClassUnknown and are shown as the driver's text form,
// which is right: a range, a composite or somebody's custom domain is still
// readable, and pretending it is a string would invite editing it.
func classFor(engine, typeName string) Class {
	if dialectOf(engine) == MySQL {
		return mysqlClass(typeName)
	}
	return postgresClass(typeName)
}

func postgresClass(name string) Class {
	n := strings.ToLower(name)
	// pgx names array types with a leading underscore: _int4 is int4[].
	if strings.HasPrefix(n, "_") || strings.HasSuffix(n, "[]") {
		return ClassArray
	}
	switch n {
	case "bool":
		return ClassBool
	case "int2", "int4", "float4", "float8", "oid":
		return ClassNumber
	case "int8", "numeric", "money":
		// int8 is a JavaScript number hazard and numeric is exact by
		// definition. Both travel as strings. See the note on Value.
		return ClassDecimal
	case "text", "varchar", "bpchar", "char", "name", "citext", "xml", "inet", "cidr", "macaddr", "macaddr8":
		return ClassString
	case "uuid":
		return ClassUUID
	case "json", "jsonb":
		return ClassJSON
	case "date":
		return ClassDate
	case "time", "timetz":
		return ClassTime
	case "timestamp", "timestamptz":
		return ClassDateTime
	case "interval":
		return ClassInterval
	case "bytea":
		return ClassBinary
	}
	return ClassUnknown
}

func mysqlClass(name string) Class {
	n := strings.ToUpper(name)
	switch n {
	case "TINYINT", "SMALLINT", "MEDIUMINT", "INT", "INTEGER", "YEAR",
		"UNSIGNED TINYINT", "UNSIGNED SMALLINT", "UNSIGNED MEDIUMINT", "UNSIGNED INT",
		"FLOAT", "DOUBLE":
		// TINYINT is where BOOLEAN lives in MySQL and the protocol does not
		// say which one the column meant, so it stays a number. The table
		// viewer knows better, from introspection, and says BOOLEAN there.
		return ClassNumber
	case "BIGINT", "UNSIGNED BIGINT", "DECIMAL", "NUMERIC":
		return ClassDecimal
	case "CHAR", "VARCHAR", "TEXT", "TINYTEXT", "MEDIUMTEXT", "LONGTEXT", "ENUM", "SET":
		return ClassString
	case "JSON":
		return ClassJSON
	case "DATE":
		return ClassDate
	case "TIME":
		return ClassTime
	case "DATETIME", "TIMESTAMP":
		return ClassDateTime
	case "BINARY", "VARBINARY", "BLOB", "TINYBLOB", "MEDIUMBLOB", "LONGBLOB", "BIT", "GEOMETRY":
		return ClassBinary
	}
	return ClassUnknown
}
