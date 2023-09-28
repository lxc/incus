package main

type internalSQLDump struct {
	Text string `json:"text" yaml:"text"`
}

type internalSQLQuery struct {
	Database string `json:"database" yaml:"database"`
	Query    string `json:"query" yaml:"query"`
}
type internalSQLBatch struct {
	Results []internalSQLResult
}

type internalSQLResult struct {
	Type         string   `json:"type" yaml:"type"`
	Columns      []string `json:"columns" yaml:"columns"`
	Rows         [][]any  `json:"rows" yaml:"rows"`
	RowsAffected int64    `json:"rows_affected" yaml:"rows_affected"`
}
