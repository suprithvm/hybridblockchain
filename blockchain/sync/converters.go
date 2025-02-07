package sync

import (
	"blockchain-core/blockchain"
	pb "blockchain-core/blockchain/sync/proto"
	"encoding/json"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// ConvertBlockToProto converts a blockchain.Block to a protobuf BlockResponse
func ConvertBlockToProto(block *blockchain.Block) *pb.BlockResponse {
	if block == nil {
		return nil
	}

	// Serialize the entire block
	blockData, err := json.Marshal(block)
	if err != nil {
		return nil
	}

	// Get transactions
	txs := block.Body.Transactions.GetAllTransactions()
	txData := make([][]byte, 0, len(txs))
	for _, tx := range txs {
		data, err := json.Marshal(tx)
		if err != nil {
			continue
		}
		txData = append(txData, data)
	}

	return &pb.BlockResponse{
		Height:          block.Header.BlockNumber,
		BlockData:       blockData,
		TransactionData: txData,
		MerkleRoot:      block.Header.MerkleRoot,
		StateRoot:       block.Header.StateRoot,
		Timestamp:       timestamppb.New(time.Unix(block.Header.Timestamp, 0)),
	}
}

// ConvertProtoToBlock converts a protobuf BlockResponse to a blockchain.Block
func ConvertProtoToBlock(resp *pb.BlockResponse) *blockchain.Block {
	if resp == nil {
		return nil
	}

	// Deserialize the block
	var block blockchain.Block
	if err := json.Unmarshal(resp.BlockData, &block); err != nil {
		return nil
	}

	// Initialize transactions trie if it's nil
	if block.Body == nil {
		block.Body = &blockchain.BlockBody{}
	}
	if block.Body.Transactions == nil {
		block.Body.Transactions = blockchain.NewPatriciaTrie()
	}

	// Add transactions
	for _, txData := range resp.TransactionData {
		var tx blockchain.Transaction
		if err := json.Unmarshal(txData, &tx); err != nil {
			continue
		}
		block.Body.Transactions.Insert(tx)
	}

	return &block
}

// ConvertUTXOToProto converts a blockchain.UTXO to protobuf UTXOResponse
func ConvertUTXOToProto(utxo *blockchain.UTXO, chunkNumber uint32, totalChunks uint32) *pb.UTXOResponse {
	if utxo == nil {
		return nil
	}

	data, err := json.Marshal(utxo)
	if err != nil {
		return nil
	}

	return &pb.UTXOResponse{
		ChunkNumber: chunkNumber,
		UtxoData:    data,
		TotalChunks: totalChunks,
	}
}

// ConvertProtoToUTXO converts a protobuf UTXOResponse to blockchain.UTXO
func ConvertProtoToUTXO(resp *pb.UTXOResponse) (*blockchain.UTXO, error) {
	if resp == nil {
		return nil, nil
	}

	var utxo blockchain.UTXO
	if err := json.Unmarshal(resp.UtxoData, &utxo); err != nil {
		return nil, err
	}

	return &utxo, nil
}

// ConvertTransactionToProto converts a blockchain.Transaction to protobuf Transaction
func ConvertTransactionToProto(tx *blockchain.Transaction) (*pb.Transaction, error) {
	if tx == nil {
		return nil, nil
	}

	data, err := json.Marshal(tx)
	if err != nil {
		return nil, err
	}

	return &pb.Transaction{
		TransactionData: data,
		TransactionHash: tx.Hash(),
		Timestamp:       timestamppb.New(time.Unix(tx.Timestamp, 0)),
	}, nil
}

// ConvertProtoToTransaction converts a protobuf Transaction to blockchain.Transaction
func ConvertProtoToTransaction(tx *pb.Transaction) (*blockchain.Transaction, error) {
	if tx == nil {
		return nil, nil
	}

	var transaction blockchain.Transaction
	if err := json.Unmarshal(tx.TransactionData, &transaction); err != nil {
		return nil, err
	}

	return &transaction, nil
}
