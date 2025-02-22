package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"time"
	"regexp"

	"github.com/cybertec-postgresql/pgwatch/v3/api"
	"github.com/destrex271/pgwatch3_rpc_server/sinks"
)

type DuckDBReceiver struct {
	Ctx       context.Context
	Conn      *sql.DB
	DBName    string
	TableName string
	sinks.SyncMetricHandler
}

func (dbr *DuckDBReceiver) initializeTable() {
	// Allow only alphanumeric and underscores in table names
	validateTableName := regexp.MustCompile(`^[a-zA-Z0-9_]+$`)
	if !validateTableName.MatchString(dbr.TableName) {
		log.Fatal("Invalid table name: potential SQL injection risk")
	}

	createTableQuery := "CREATE TABLE IF NOT EXISTS " + dbr.TableName + "(dbname VARCHAR, metric_name VARCHAR, data JSON, custom_tags JSON, metric_def JSON, timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY (dbname, timestamp))"
	_, err := dbr.Conn.Exec(createTableQuery)
	if err != nil {
		log.Fatal(err)
	}
	log.Print("Table successfully created")
}

func NewDBDuckReceiver(databaseName string, tableName string) (dbr *DuckDBReceiver, err error) {
	// close fatally if table isnt created, or if receiver isnt initailized properly
	db, err := sql.Open("duckdb", databaseName)
	if err != nil {
		log.Fatal(err)
	}

	dbr = &DuckDBReceiver{
		Conn:              db,
		DBName:            databaseName,
		TableName:         tableName,
		Ctx:               context.Background(),
		SyncMetricHandler: sinks.NewSyncMetricHandler(1024),
	}

	dbr.initializeTable()
	return dbr, nil
}
func (r *DuckDBReceiver) InsertMeasurements(data *api.MeasurementEnvelope, ctx context.Context) error {
	metricDef, _ := json.Marshal(data.MetricDef)
	customTagsJSON, _ := json.Marshal(data.CustomTags)
	// log.Println("Data:: ", data)

	// use direct SQL approach - just use the existing connection with the standard insert statement
	tx, err := r.Conn.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("Error beginning transaction: %v", err)
		return err
	}

	stmt, err := tx.Prepare("INSERT INTO " + r.TableName +
		" (dbname, metric_name, data, custom_tags, metric_def, timestamp) VALUES (?, ?, ?, ?, ?, ?)")
	if err != nil {
		log.Printf("error from preparing statement: %v", err)
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	for _, measurement := range data.Data {
		measurementJSON, err := json.Marshal(measurement)
		if err != nil {
			log.Printf("error from marshal measurement: %v", err)
			tx.Rollback()
			return err
		}

		_, err = stmt.Exec(
			data.DBName,
			data.MetricName,
			string(measurementJSON),
			string(customTagsJSON),
			string(metricDef),
			time.Now(),
		)
		if err != nil {
			log.Printf("error from insert: %v", err)
			tx.Rollback()
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		log.Printf("error from committing transaction: %v", err)
		return err
	}
	return nil
}

func (r *DuckDBReceiver) UpdateMeasurements(msg *api.MeasurementEnvelope, logMsg *string) error {

	log.Printf("Received measurement. DBName: '%s', MetricName: '%s', DataPoints: %d",
		msg.DBName, msg.MetricName, len(msg.Data))

	if len(msg.DBName) == 0 {
		*logMsg = "empty database name"
		return errors.New(*logMsg)
	}

	if len(msg.MetricName) == 0 {
		*logMsg = "empty metric name"
		return errors.New(*logMsg)
	}

	if len(msg.Data) == 0 {
		*logMsg = "no measurements"
		return errors.New(*logMsg)
	}

	err := r.InsertMeasurements(msg, context.Background())
	if err != nil {
		*logMsg = err.Error()
		return err
	}

	log.Println("[INFO]: Inserted batch at : " + time.Now().String())
	*logMsg = "[INFO]: Successfully inserted batch!"
	return nil
}
