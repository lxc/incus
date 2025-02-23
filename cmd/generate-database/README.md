# `generate-database`

## Introduction

`generate-database` is a database statement and associated `go` function generator
for Incus and related projects. `generate-database` utilizes `go`'s code generation
directives (`//go:generate ...`) alongside go's [ast](https://pkg.go.dev/go/ast)
and [types](https://pkg.go.dev/go/types) packages for parsing the syntax tree for
go structs and variables. We use `generate-database` for the majority of our
SQL statements and database interactions on the `go` side for consistency and
predictability.

## Usage

### Initialization

#### Package global

Once per package, that uses `generate-database` for generation of database
statements and associated `go` functions, `generate-database` needs to be invoked
using the following `go:generate` instruction:

```go
//go:generate generate-database db mapper generate
```

This will initiate a call to `generate-database db mapper generate`,
which will then search for `//generate-database:mapper` directives in the same file
and process those.

#### File

Generally the first thing we will want to do for any newly generated file is to
ensure the file has been cleared of content:

```go
//generate-database:mapper target instances.mapper.go
//generate-database:mapper reset -i -b "//go:build linux && cgo && !agent"
```

### Generation Directive Arguments

The generation directive aruments have the following form:

`//generate-database:mapper <command> flags <kind> <args...>`

The following flags are available:

* `--build` / `-b`: build comment to include (commands: `reset`)
* `--interface` / `-i`: create interface files (commands: `reset`, `method`)
* `--entity` / `-e`: database entity to generate the method or statement for (commands: `stmt`, `method`)

Example:

* `//generate-database:mapper stmt -e instance objects table=table_name`

The `table` key can be used to override the generated table name for a specified one.

* `//generate-database:mapper method -i -e instance Create references=Config,Device`

For some tables (defined below under [Additional Information](#Additional-Information) as [EntityTable](#EntityTable), the `references=<ReferenceEntity>` key can be provided with the name of
a [ReferenceTable](#ReferenceTable) or [MapTable](#MapTable) struct. This directive would produce `CreateInstance` in addition to `CreateInstanceConfig` and `CreateInstanceDevices`:

* `//generate-database:mapper method -i -e instance_profile Create struct=Instance`
* `//generate-database:mapper method -i -e instance_profile Create struct=Profile`

For some tables (defined below under [Additional Information](#Additional-Information) as [AssociationTable](#AssociationTable), `method` declarations must
include a `struct=<Entity>` to indicate the directionality of the function. An invocation can be called for each direction.
This would produce `CreateInstanceProfiles` and `CreateProfileInstances` respectively.

### SQL Statement Generation

SQL generation supports the following SQL statement types:

Type                                  | Description
:---                                  | :----
`objects`                             | Creates a basic SELECT statement of the form `SELECT <columns> FROM <table> ORDER BY <columns>`.
`objects-by-<FIELD>-and-<FIELD>...`   | Parses a pre-existing SELECT statement variable declaration of the form produced by`objects`, and appends a `WHERE` clause with the given fields located in the associated struct. Specifically looks for a variable declaration of the form `var <entity>Objects = RegisterStmt("SQL String")`
`create`                              | Creates a basic INSERT statement of the form `INSERT INTO <table> VALUES`.
`create-or-replace`                   | Creates a basic INSERT statement of the form `INSERT OR REPLACE INTO <table> VALUES`.
`delete-by-<FIELD>-and-<FIELD>...`    | Creates a DELETE statement of the form `DELETE FROM <table> WHERE <constraint>` where the constraint is based on the given fields of the associated struct.
`id`                                  | Creates a basic SELECT statement that returns just the internal ID of the table.
`rename`                              | Creates an UPDATE statement that updates the primary key of a table: `UPDATE <table> SET <primary key> WHERE <primary key = ?>`.
`update`                              | Creates an UPDATE statement of the form `UPDATE <table> SET <all columns> WHERE <primary key = ?>`.

#### Examples

```go
//generate-database:mapper stmt -e instance objects
//generate-database:mapper stmt -e instance objects-by-Name-and-Project
//generate-database:mapper stmt -e instance create
//generate-database:mapper stmt -e instance update
//generate-database:mapper stmt -e instance delete-by-Name-and-Project
```

#### Statement Related Go Tags

There are several tags that can be added to fields of a struct that will be parsed by the `ast` package.

Tag                         | Description
:--                         | :----
`sql=<table>.<column>`      | Supply an explicit table and column name to use for this struct field.
`coalesce=<value>`          | Generates a SQL coalesce function with the given value `coalesce(<column>, value)`.
`order=yes`                 | Override the default `ORDER BY` columns with all fields specifying this tag.
`join=<joinTable.column>`   | Applies a `JOIN` of the form `JOIN <joinTable> ON <table>.<joinTable_id> = <joinTable.id>`.
`leftjoin=<table.column>`   | Applies a `LEFT JOIN` of the same form as a `JOIN`.
`joinon=<table>.<column>`   | Overrides the default `JOIN ON` clause with the given table and column, replacing `<table>.<joinTable_id>` above.
`primary=yes`               | Assigns column associated with the field to be sufficient for returning a row from the table. Will default to `Name` if unspecified. Fields with this key will be included in the default 'ORDER BY' clause.
`omit=<Stmt Types>`         | Omits a given field from consideration for the comma separated list of statement types (`create`, `objects-by-Name`, `update`).
`ignore=yes`                | Outright ignore the struct field as though it does not exist.
`marshal=yes`               | Marshal/Unmarshal data into the field. The column must be a TEXT column and the type must implement both `query.Marshal` and `query.Unmarshal`. This works for entity tables only, and not for association or mapping tables.

### Go Function Generation

Go function generation supports the following types:

Type                                | Description
:---                                | :----
`GetMany`                           | Return a slice of structs for all rows in a table matching the filter.
`GetOne`                            | Return a single struct corresponding to a row with the given primary keys. Depends on `GetMany`.
`ID`                                | Return the ID column from the table corresponding to the given primary keys.
`Exists`                            | Returns whether there is an row in the table with the given primary keys. Depends on `ID.`
`Create`                            | Insert a row from the given struct into the table if not already present. Depends on `Exists`
`CreateOrReplace`                   | Insert a row from the given struct into the table, regardless of if an entry already exists.
`Rename`                            | Update the primary key for a table row.
`Update`                            | Update the columns at a given row, specified by primary key.
`DeleteOne`                         | Delete exactly one row from the table.
`DeleteMany`                        | Delete one or more rows from the table.

```go
//generate-database:mapper method -i -e instance GetMany
//generate-database:mapper method -i -e instance GetOne
//generate-database:mapper method -i -e instance ID
//generate-database:mapper method -i -e instance Exist
//generate-database:mapper method -i -e instance Create
//generate-database:mapper method -i -e instance Update
//generate-database:mapper method -i -e instance DeleteOne-by-Project-and-Name
//generate-database:mapper method -i -e instance DeleteMany-by-Name
```

### Additional Information

All structs should have an `ID` field, as well as an additional `Filter` struct prefixed with the original struct name.
This should include any fields that should be considered for filtering in `WHERE` clauses.
These fields should be pointers to facilitate omission and inclusion without setting default values.

Example:

```go
type Instance struct {
  ID int
  Name string
  Project string
}

type InstanceFilter struct {
  Name *string
  Project *string
}
```

`generate-database` will handle parsing of structs differently based on the composition of the struct in four different ways.

Non-`EntityType` structs will only support `GetMany`, `Create`, `Update`, and `Delete` functions.

### EntityTable

Most structs will get treated this way, and represent a normal table.

* If a table has an associated table for which a `ReferenceTable` or `MapTable` as defined below is applicable, functions specific to this entity can be generated by
including a comma separated list to `references=<OtherEntity>` in the code generation directive for `GetMany`, `Create`, or `Update` directives.

* The `Create` method directive for `EntityTable` will expect on the `ID` and `Exist` method directives to be present.

* All `CREATE`, `UPDATE`, and `DELETE` statements that include a joined table will expect a `var <entity>ID = RegisterStmt('SQL String')` to exist for the joining table.

### ReferenceTable

A struct that contains a field named `ReferenceID` will be parsed this way.
`generate-database` will use this struct to generate more abstract SQL statements and functions of the form `<parent_table>_<this_table>`.

Real world invocation of these statements and functions should be done through an `EntityTable` `method` call with the tag `references=<ThisStruct>`. This `EntityTable` will replace the `<parent_table>` above.

Example:

```go
//generate-database:mapper stmt -e device create
//generate-database:mapper method -e device Create

type Device struct {
  ID int
  ReferenceID int
  Name string
  Type string
}

//...
//generate-database:mapper method -e instance Create references=Device
// This will produce a function called `CreateInstanceDevices`.
```

### MapTable

This is a special type of `ReferenceTable` with fields named `Key` and `Value`.
On the SQL side, this is treated exactly like a `ReferenceTable`, but on the `go` side, the return values will be a map.

Example:

```go
//generate-database:mapper stmt -e config create
//generate-database:mapper method -e config Create

type Config struct {
  ID int
  ReferenceID int
  Key string
  Value string
}

//...
//generate-database:mapper method -e instance Create references=Config
// This will produce a function called `CreateInstanceConfig`, which will return a `map[string]string`.
```

### AssociationTable

This is a special type of table that contains two fields of the form `<Entity>ID`, where `<Entity>` corresponds to two other structs present in the same package.
This will generate code for compound tables of the form `<entity1>_<entity2>` that are generally used to associate two tables together by their IDs.

`method` generation declarations for these statements should include a `struct=<Entity>` to indicate the directionality of the function.
An invocation can be called for each direction.

Example:

```go
//generate-database:mapper method -i -e instance_profile Create struct=Instance
//generate-database:mapper method -i -e instance_profile Create struct=Profile

type InstanceProfile struct {
  InstanceID int
  ProfileID int
}
```
