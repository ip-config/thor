// Copyright (c) 2018 The VeChainThor developers

// Distributed under the GNU Lesser General Public License v3.0 software license, see the accompanying
// file LICENSE or <https://www.gnu.org/licenses/lgpl-3.0.html>

package logdb

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math/big"

	sqlite3 "github.com/mattn/go-sqlite3"
	"github.com/vechain/thor/block"
	"github.com/vechain/thor/thor"
	"github.com/vechain/thor/tx"
)

var configBlockNumKey = "blockNum"

type LogDB struct {
	path          string
	db            *sql.DB
	driverVersion string
}

// New create or open log db at given path.
func New(path string) (logDB *LogDB, err error) {
	db, err := sql.Open("sqlite3", path+"?_journal=wal&cache=shared")
	if err != nil {
		return nil, err
	}
	defer func() {
		if logDB == nil {
			db.Close()
		}
	}()

	// to avoid 'database is locked' error
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(configTableSchema + eventTableSchema + transferTableSchema); err != nil {
		return nil, err
	}

	driverVer, _, _ := sqlite3.Version()
	return &LogDB{
		path,
		db,
		driverVer,
	}, nil
}

// NewMem create a log db in ram.
func NewMem() (*LogDB, error) {
	return New(":memory:")
}

// Close close the log db.
func (db *LogDB) Close() {
	db.db.Close()
}

func (db *LogDB) Path() string {
	return db.path
}

func (db *LogDB) Prepare(header *block.Header) *BlockBatch {
	return &BlockBatch{
		db:     db.db,
		header: header,
	}
}

func (db *LogDB) FilterEvents(ctx context.Context, filter *EventFilter) ([]*Event, error) {
	if filter == nil {
		return db.queryEvents(ctx, "SELECT * FROM event")
	}
	var args []interface{}
	stmt := "SELECT * FROM event WHERE 1"
	condition := "blockNumber"
	if filter.Range != nil {
		if filter.Range.Unit == Time {
			condition = "blockTime"
		}
		args = append(args, filter.Range.From)
		stmt += " AND " + condition + " >= ? "
		if filter.Range.To >= filter.Range.From {
			args = append(args, filter.Range.To)
			stmt += " AND " + condition + " <= ? "
		}
	}
	for i, criteria := range filter.CriteriaSet {
		if i == 0 {
			stmt += " AND ( 1"
		} else {
			stmt += " OR ( 1"
		}
		if criteria.Address != nil {
			args = append(args, criteria.Address.Bytes())
			stmt += " AND address = ? "
		}
		for j, topic := range criteria.Topics {
			if topic != nil {
				args = append(args, topic.Bytes())
				stmt += fmt.Sprintf(" AND topic%v = ?", j)
			}
		}
		stmt += ")"
	}

	if filter.Order == DESC {
		stmt += " ORDER BY blockNumber DESC,eventIndex DESC "
	} else {
		stmt += " ORDER BY blockNumber ASC,eventIndex ASC "
	}

	if filter.Options != nil {
		stmt += " limit ?, ? "
		args = append(args, filter.Options.Offset, filter.Options.Limit)
	}
	return db.queryEvents(ctx, stmt, args...)
}

func (db *LogDB) FilterTransfers(ctx context.Context, filter *TransferFilter) ([]*Transfer, error) {
	if filter == nil {
		return db.queryTransfers(ctx, "SELECT * FROM transfer")
	}
	var args []interface{}
	stmt := "SELECT * FROM transfer WHERE 1"
	condition := "blockNumber"
	if filter.Range != nil {
		if filter.Range.Unit == Time {
			condition = "blockTime"
		}
		args = append(args, filter.Range.From)
		stmt += " AND " + condition + " >= ? "
		if filter.Range.To >= filter.Range.From {
			args = append(args, filter.Range.To)
			stmt += " AND " + condition + " <= ? "
		}
	}
	if filter.TxID != nil {
		args = append(args, filter.TxID.Bytes())
		stmt += " AND txID = ? "
	}
	length := len(filter.CriteriaSet)
	if length > 0 {
		for i, criteria := range filter.CriteriaSet {
			if i == 0 {
				stmt += " AND (( 1 "
			} else {
				stmt += " OR ( 1 "
			}
			if criteria.TxOrigin != nil {
				args = append(args, criteria.TxOrigin.Bytes())
				stmt += " AND txOrigin = ? "
			}
			if criteria.Sender != nil {
				args = append(args, criteria.Sender.Bytes())
				stmt += " AND sender = ? "
			}
			if criteria.Recipient != nil {
				args = append(args, criteria.Recipient.Bytes())
				stmt += " AND recipient = ? "
			}
			if i == length-1 {
				stmt += " )) "
			} else {
				stmt += " ) "
			}
		}
	}
	if filter.Order == DESC {
		stmt += " ORDER BY blockNumber DESC,transferIndex DESC "
	} else {
		stmt += " ORDER BY blockNumber ASC,transferIndex ASC "
	}
	if filter.Options != nil {
		stmt += " limit ?, ? "
		args = append(args, filter.Options.Offset, filter.Options.Limit)
	}
	return db.queryTransfers(ctx, stmt, args...)
}

func (db *LogDB) queryEvents(ctx context.Context, stmt string, args ...interface{}) ([]*Event, error) {
	rows, err := db.db.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*Event
	for rows.Next() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		var (
			blockNumber uint32
			index       uint32
			blockID     []byte
			blockTime   uint64
			txID        []byte
			txOrigin    []byte
			clauseIndex uint32
			address     []byte
			topics      [5][]byte
			data        []byte
		)
		if err := rows.Scan(
			&blockNumber,
			&index,
			&blockID,
			&blockTime,
			&txID,
			&txOrigin,
			&clauseIndex,
			&address,
			&topics[0],
			&topics[1],
			&topics[2],
			&topics[3],
			&topics[4],
			&data,
		); err != nil {
			return nil, err
		}
		event := &Event{
			BlockNumber: blockNumber,
			Index:       index,
			BlockID:     thor.BytesToBytes32(blockID),
			BlockTime:   blockTime,
			TxID:        thor.BytesToBytes32(txID),
			TxOrigin:    thor.BytesToAddress(txOrigin),
			ClauseIndex: clauseIndex,
			Address:     thor.BytesToAddress(address),
			Data:        data,
		}
		for i, topic := range topics {
			if len(topic) > 0 {
				h := thor.BytesToBytes32(topic)
				event.Topics[i] = &h
			}
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func (db *LogDB) queryTransfers(ctx context.Context, stmt string, args ...interface{}) ([]*Transfer, error) {
	rows, err := db.db.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var transfers []*Transfer
	for rows.Next() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		var (
			blockNumber uint32
			index       uint32
			blockID     []byte
			blockTime   uint64
			txID        []byte
			txOrigin    []byte
			clauseIndex uint32
			sender      []byte
			recipient   []byte
			amount      []byte
		)
		if err := rows.Scan(
			&blockNumber,
			&index,
			&blockID,
			&blockTime,
			&txID,
			&txOrigin,
			&clauseIndex,
			&sender,
			&recipient,
			&amount,
		); err != nil {
			return nil, err
		}
		trans := &Transfer{
			BlockNumber: blockNumber,
			Index:       index,
			BlockID:     thor.BytesToBytes32(blockID),
			BlockTime:   blockTime,
			TxID:        thor.BytesToBytes32(txID),
			TxOrigin:    thor.BytesToAddress(txOrigin),
			ClauseIndex: clauseIndex,
			Sender:      thor.BytesToAddress(sender),
			Recipient:   thor.BytesToAddress(recipient),
			Amount:      new(big.Int).SetBytes(amount),
		}
		transfers = append(transfers, trans)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return transfers, nil
}

func (db *LogDB) QueryLastBlockNumber() (uint32, error) {
	row := db.db.QueryRow("SELECT value FROM config WHERE key=?", configBlockNumKey)
	var data []byte
	if err := row.Scan(&data); err != nil {
		if sql.ErrNoRows == err {
			return 0, nil
		}
		return 0, err
	}
	return binary.BigEndian.Uint32(data), nil
}

func topicValue(topic *thor.Bytes32) []byte {
	if topic == nil {
		return nil
	}
	return topic.Bytes()
}

type BlockBatch struct {
	db        *sql.DB
	header    *block.Header
	events    []*Event
	transfers []*Transfer
}

func (bb *BlockBatch) execInTx(proc func(*sql.Tx) error) (err error) {
	tx, err := bb.db.Begin()
	if err != nil {
		return err
	}
	if err := proc(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (bb *BlockBatch) Commit() error {
	return bb.execInTx(func(tx *sql.Tx) error {
		// skip on initializing genesis
		if bb.header.Number() > 0 {
			if _, err := tx.Exec("DELETE from event where blockNumber >= ?", bb.header.Number()); err != nil {
				return err
			}
			if _, err := tx.Exec("DELETE from transfer where blockNumber >= ?", bb.header.Number()); err != nil {
				return err
			}
			var b4 [4]byte
			binary.BigEndian.PutUint32(b4[:], bb.header.Number())

			tx.Exec("INSERT OR REPLACE INTO config(key, value) VALUES(?,?)",
				configBlockNumKey,
				b4[:],
			)
		}

		for _, event := range bb.events {
			if _, err := tx.Exec("INSERT OR REPLACE INTO event(blockNumber, eventIndex, blockID, blockTime, txID, txOrigin, clauseIndex, address, topic0, topic1, topic2, topic3, topic4, data) VALUES ( ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);",
				event.BlockNumber,
				event.Index,
				event.BlockID.Bytes(),
				event.BlockTime,
				event.TxID.Bytes(),
				event.TxOrigin.Bytes(),
				event.ClauseIndex,
				event.Address.Bytes(),
				topicValue(event.Topics[0]),
				topicValue(event.Topics[1]),
				topicValue(event.Topics[2]),
				topicValue(event.Topics[3]),
				topicValue(event.Topics[4]),
				event.Data,
			); err != nil {
				return err
			}
		}

		for _, transfer := range bb.transfers {
			if _, err := tx.Exec("INSERT OR REPLACE INTO transfer(blockNumber, transferIndex, blockID, blockTime, txID, txOrigin, clauseIndex, sender, recipient, amount) VALUES ( ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);",
				transfer.BlockNumber,
				transfer.Index,
				transfer.BlockID.Bytes(),
				transfer.BlockTime,
				transfer.TxID.Bytes(),
				transfer.TxOrigin.Bytes(),
				transfer.ClauseIndex,
				transfer.Sender.Bytes(),
				transfer.Recipient.Bytes(),
				transfer.Amount.Bytes(),
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func (bb *BlockBatch) ForTransaction(txID thor.Bytes32, txOrigin thor.Address) struct {
	Insert func(tx.Events, tx.Transfers, uint32) *BlockBatch
} {
	return struct {
		Insert func(events tx.Events, transfers tx.Transfers, clauseIndex uint32) *BlockBatch
	}{
		func(events tx.Events, transfers tx.Transfers, clauseIndex uint32) *BlockBatch {
			for _, event := range events {
				bb.events = append(bb.events, newEvent(bb.header, uint32(len(bb.events)), txID, txOrigin, clauseIndex, event))
			}
			for _, transfer := range transfers {
				bb.transfers = append(bb.transfers, newTransfer(bb.header, uint32(len(bb.transfers)), txID, txOrigin, clauseIndex, transfer))
			}
			return bb
		},
	}
}
