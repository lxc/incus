package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"time"
)

// Subset of JMeter CSV log format that are required by Jenkins performance
// plugin
// (see http://jmeter.apache.org/usermanual/listeners.html#csvlogformat)
var csvFields = []string{
	"timeStamp", // in milliseconds since 1/1/1970
	"elapsed",   // in milliseconds
	"label",
	"responseCode",
	"success", // "true" or "false"
}

// CSVReport reads/writes a CSV report file.
type CSVReport struct {
	Filename string

	records [][]string
}

// Load reads current content of the filename and loads records.
func (r *CSVReport) Load() error {
	file, err := os.Open(r.Filename)
	if err != nil {
		return err
	}

	defer func() { _ = file.Close() }()

	reader := csv.NewReader(file)
	for line := 1; err != io.EOF; line++ {
		record, err := reader.Read()
		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}

		err = r.addRecord(record)
		if err != nil {
			return err
		}
	}
	logf("Loaded report file %s", r.Filename)
	return nil
}

// Write writes current records to file.
func (r *CSVReport) Write() error {
	file, err := os.OpenFile(r.Filename, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}

	defer func() { _ = file.Close() }()

	writer := csv.NewWriter(file)
	err = writer.WriteAll(r.records)
	if err != nil {
		return err
	}

	logf("Written report file %s", r.Filename)
	return file.Close()
}

// AddRecord adds a record to the report.
func (r *CSVReport) AddRecord(label string, elapsed time.Duration) error {
	if len(r.records) == 0 {
		err := r.addRecord(csvFields)
		if err != nil {
			return err
		}
	}

	record := []string{
		fmt.Sprintf("%d", time.Now().UnixNano()/int64(time.Millisecond)), // timestamp
		fmt.Sprintf("%d", elapsed/time.Millisecond),
		label,
		"",     // responseCode is not used
		"true", // success"
	}

	return r.addRecord(record)
}

func (r *CSVReport) addRecord(record []string) error {
	if len(record) != len(csvFields) {
		return fmt.Errorf("Invalid number of fields : %q", record)
	}

	r.records = append(r.records, record)
	return nil
}
