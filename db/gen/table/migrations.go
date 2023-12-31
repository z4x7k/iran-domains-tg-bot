//
// Code generated by go-jet DO NOT EDIT.
//
// WARNING: Changes to this file may cause incorrect behavior
// and will be lost if the code is regenerated
//

package table

import (
	"github.com/go-jet/jet/v2/sqlite"
)

var Migrations = newMigrationsTable("", "migrations", "")

type migrationsTable struct {
	sqlite.Table

	// Columns
	ID        sqlite.ColumnInteger
	VersionID sqlite.ColumnInteger
	IsApplied sqlite.ColumnInteger
	Tstamp    sqlite.ColumnTimestamp

	AllColumns     sqlite.ColumnList
	MutableColumns sqlite.ColumnList
}

type MigrationsTable struct {
	migrationsTable

	EXCLUDED migrationsTable
}

// AS creates new MigrationsTable with assigned alias
func (a MigrationsTable) AS(alias string) *MigrationsTable {
	return newMigrationsTable(a.SchemaName(), a.TableName(), alias)
}

// Schema creates new MigrationsTable with assigned schema name
func (a MigrationsTable) FromSchema(schemaName string) *MigrationsTable {
	return newMigrationsTable(schemaName, a.TableName(), a.Alias())
}

// WithPrefix creates new MigrationsTable with assigned table prefix
func (a MigrationsTable) WithPrefix(prefix string) *MigrationsTable {
	return newMigrationsTable(a.SchemaName(), prefix+a.TableName(), a.TableName())
}

// WithSuffix creates new MigrationsTable with assigned table suffix
func (a MigrationsTable) WithSuffix(suffix string) *MigrationsTable {
	return newMigrationsTable(a.SchemaName(), a.TableName()+suffix, a.TableName())
}

func newMigrationsTable(schemaName, tableName, alias string) *MigrationsTable {
	return &MigrationsTable{
		migrationsTable: newMigrationsTableImpl(schemaName, tableName, alias),
		EXCLUDED:        newMigrationsTableImpl("", "excluded", ""),
	}
}

func newMigrationsTableImpl(schemaName, tableName, alias string) migrationsTable {
	var (
		IDColumn        = sqlite.IntegerColumn("id")
		VersionIDColumn = sqlite.IntegerColumn("version_id")
		IsAppliedColumn = sqlite.IntegerColumn("is_applied")
		TstampColumn    = sqlite.TimestampColumn("tstamp")
		allColumns      = sqlite.ColumnList{IDColumn, VersionIDColumn, IsAppliedColumn, TstampColumn}
		mutableColumns  = sqlite.ColumnList{VersionIDColumn, IsAppliedColumn, TstampColumn}
	)

	return migrationsTable{
		Table: sqlite.NewTable(schemaName, tableName, alias, allColumns...),

		//Columns
		ID:        IDColumn,
		VersionID: VersionIDColumn,
		IsApplied: IsAppliedColumn,
		Tstamp:    TstampColumn,

		AllColumns:     allColumns,
		MutableColumns: mutableColumns,
	}
}
