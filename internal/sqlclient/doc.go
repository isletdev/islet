// Package sqlclient is the SQL client's engine: connections and pools,
// sessions, statement splitting and classification, introspection, execution
// with a row cap and a real cancel, and the tables behind saved connections,
// saved queries and history.
//
// It knows nothing about HTTP. The API package validates, authorises and
// marshals; everything here is testable without a server, and most of it
// without a database.
//
// The specification this implements is docs/SQL_CLIENT.md. Where the two
// disagree, the specification is the intent and this is the fact; reconcile
// them rather than letting them drift.
package sqlclient
