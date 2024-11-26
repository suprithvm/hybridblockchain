package blockchain

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"  // Updated import
	"github.com/libp2p/go-libp2p/core/network"  // Updated import
	"github.com/libp2p/go-libp2p/core/peer"  // Updated import
	"github.com/multiformats/go-multiaddr"
)

// Node represents a P2P node in the blockchain network
type Node struct {
	Host host.Host
}

// NewNode initializes a new P2P node
func NewNode(listenAddr string) (*Node, error) {
	// Create a new libp2p host
	h, err := libp2p.New(libp2p.ListenAddrStrings(listenAddr))
	if err != nil {
		return nil, err
	}

	// Print the node's peer ID and addresses
	fmt.Printf("Node ID: %s\n", h.ID().String())
	for _, addr := range h.Addrs() {
		fmt.Printf("Listening on: %s/p2p/%s\n", addr, h.ID().String())
	}

	node := &Node{Host: h}
	node.setupStreamHandler()

	return node, nil
}

// setupStreamHandler sets up a handler for incoming streams
func (n *Node) setupStreamHandler() {
    // Handle block messages.
    n.Host.SetStreamHandler("/blockchain/1.0.0/block", func(s network.Stream) {
        defer s.Close()

        // Use a larger buffer for block data
        buf := make([]byte, 4096) // Increased buffer size
        fullData := []byte{}

        for {
            n, err := s.Read(buf)
            if err != nil {
                if err == io.EOF {
                    break
                }
                log.Printf("Stream read error: %v", err)
                return
            }

            // Append the read data
            fullData = append(fullData, buf[:n]...)
        }

        // Deserialize the full data
        block, err := DeserializeBlock(fullData)
        if err != nil {
            log.Printf("Failed to deserialize block: %v", err)
            return
        }

        log.Printf("Received Block: %+v\n", block)
    })

    // Handle transaction messages (no changes required for this handler).
    n.Host.SetStreamHandler("/blockchain/1.0.0/transaction", func(s network.Stream) {
        defer s.Close()

        buf := make([]byte, 512)
        bytesRead, err := s.Read(buf)
        if err != nil {
            log.Println("Error reading stream:", err)
            return
        }

        tx, err := DeserializeTransaction(buf[:bytesRead])
        if err != nil {
            log.Println("Failed to deserialize transaction:", err)
            return
        }
        log.Printf("Received Transaction: %+v\n", tx)
    })

	// Add handler for validator messages
	n.Host.SetStreamHandler("/blockchain/1.0.0/validator", func(s network.Stream) {
		defer s.Close()
		buf := make([]byte, 256)
		n, err := s.Read(buf)
		if err != nil {
			log.Printf("Error reading validator stream: %v", err)
			return
		}

		// Deserialize validator data (walletAddress, hostID)
		data := string(buf[:n])
		parts := strings.Split(data, ",")
		if len(parts) != 2 {
			log.Printf("Invalid validator data received: %s", data)
			return
		}
		walletAddress, hostID := parts[0], parts[1]
		log.Printf("Received validator announcement: Wallet=%s, HostID=%s", walletAddress, hostID)
	})

}




// BroadcastBlock broadcasts a block to all peers
func (n *Node) BroadcastBlock(block Block) {
	data, err := SerializeBlock(block)
	if err != nil {
		log.Printf("Failed to serialize block: %v", err)
		return
	}

	for _, p := range n.Host.Peerstore().Peers() {
		if p == n.Host.ID() {
			continue
		}

		go func(peerID peer.ID) {
			stream, err := n.Host.NewStream(context.Background(), peerID, "/blockchain/1.0.0/block")
			if err != nil {
				log.Printf("Failed to open stream to peer %s: %v", peerID.String(), err)
				return
			}
			defer stream.Close()
			_, _ = stream.Write(data)
		}(p)
	}
}

// BroadcastTransaction broadcasts a transaction to all peers
func (n *Node) BroadcastTransaction(tx Transaction) {
	data, err := SerializeTransaction(&tx)
	if err != nil {
		log.Printf("Failed to serialize transaction: %v", err)
		return
	}

	for _, p := range n.Host.Peerstore().Peers() {
		stream, err := n.Host.NewStream(context.Background(), p, "/blockchain/1.0.0/transaction")
		if err != nil {
			log.Printf("Failed to open stream to peer %s: %v\n", p.String(), err)
			continue
		}
		defer stream.Close()
		_, _ = stream.Write(data)
	}
}







// ConnectToPeer connects to a given peer
func (n *Node) ConnectToPeer(peerAddr string) error {
	// Parse the peer multiaddress
	maddr, err := multiaddr.NewMultiaddr(peerAddr)
	if err != nil {
		return fmt.Errorf("failed to parse peer address: %w", err)
	}

	addrInfo, err := peer.AddrInfoFromP2pAddr(maddr)
	if err != nil {
		return fmt.Errorf("failed to get AddrInfo from multiaddress: %w", err)
	}

	// Connect to the peer
	if err := n.Host.Connect(context.Background(), *addrInfo); err != nil {
		return fmt.Errorf("failed to connect to peer: %w", err)
	}

	fmt.Printf("Connected to peer: %s\n", addrInfo.ID.String())
	return nil
}

// BroadcastMessage sends a string message to all peers
func (n *Node) BroadcastMessage(msg string) {
	for _, p := range n.Host.Peerstore().Peers() {
		// Avoid dialing to self
		if p == n.Host.ID() {
			continue
		}

		stream, err := n.Host.NewStream(context.Background(), p, "/blockchain/1.0.0")
		if err != nil {
			log.Printf("Failed to open stream to peer %s: %v\n", p.String(), err)
			continue
		}
		defer stream.Close()
		_, err = stream.Write([]byte(msg))
		if err != nil {
			log.Printf("Failed to send message to peer %s: %v\n", p.String(), err)
		}
	}
}



