package sql

// SQLDump represents a full database dump.
type SQLDump struct {
	Text string `json:"text" yaml:"text"`
}

// SQLQuery represents a DB query.
type SQLQuery struct {
	Database string `json:"database" yaml:"database"`
	Query    string `json:"query"    yaml:"query"`
}

// SQLBatch represents a batch result.
type SQLBatch struct {
	Results []SQLResult
}

// SQLResult reprents a query result.
type SQLResult struct {
	Type         string   `json:"type"          yaml:"type"`
	Columns      []string `json:"columns"       yaml:"columns"`
	Rows         [][]any  `json:"rows"          yaml:"rows"`
	RowsAffected int64    `json:"rows_affected" yaml:"rows_affected"`
}
