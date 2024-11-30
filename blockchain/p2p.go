package blockchain

import (
    "context"
    "encoding/json"
    "fmt"
    "io"
    "log"
    "strings"
    "time"

    "github.com/libp2p/go-libp2p"
    dht "github.com/libp2p/go-libp2p-kad-dht"
    "github.com/libp2p/go-libp2p/core/host"
    "github.com/libp2p/go-libp2p/core/network"
    "github.com/libp2p/go-libp2p/core/peer"
    "github.com/libp2p/go-libp2p/core/protocol"
)

// Node represents a P2P node in the blockchain network
type Node struct {
    Host        host.Host
    PeerManager *PeerManager
    DHT         *dht.IpfsDHT
    Blockchain  *Blockchain
}

func NewNode(listenAddr string, bootstrapPeers []peer.AddrInfo) (*Node, error) {
    host, err := libp2p.New(libp2p.ListenAddrStrings(listenAddr))
    if err != nil {
        return nil, err
    }

    dhtInstance, err := dht.New(context.Background(), host, dht.Mode(dht.ModeServer))
    if err != nil {
        return nil, err
    }

    for _, bp := range bootstrapPeers {
        if err := host.Connect(context.Background(), bp); err != nil {
            log.Printf("Failed to connect to bootstrap peer %s: %v", bp.ID, err)
        }
    }

    return &Node{
        Host:        host,
        PeerManager: NewPeerManager(),
        DHT:         dhtInstance,
        Blockchain:  &Blockchain{Chain: []Block{GenesisBlock()}},
    }, nil
}

// DiscoverPeers uses DHT to discover peers
func (n *Node) DiscoverPeers() {
    for {
        // Get peers from the DHT's routing table
        routingTable := n.DHT.RoutingTable()
        peers := routingTable.ListPeers()

        for _, p := range peers {
            if p == n.Host.ID() {
                continue
            }

            // Get peer addresses from the peerstore
            peerInfo := n.Host.Peerstore().PeerInfo(p)
            
            if !n.PeerManager.AddPeer(&peerInfo) {
                log.Printf("Failed to add peer %s: connection pool is full", p.String())
            } else {
                n.PeerManager.UpdateLastSeen(p)
                log.Printf("Discovered and added peer: %s", p.String())
            }
        }

        time.Sleep(10 * time.Second) // Adjust the interval as needed
    }
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

    // Handle incoming chain data
    n.Host.SetStreamHandler("/blockchain/1.0.0/chain", func(s network.Stream) {
        defer s.Close()
        buf := make([]byte, 4096)
        _, err := s.Read(buf)
        if err != nil {
            log.Printf("[P2P] Error reading chain data: %v", err)
            return
        }

        var receivedChain []Block
        err = json.Unmarshal(buf, &receivedChain)
        if err != nil {
            log.Printf("[P2P] Failed to deserialize chain: %v", err)
            return
        }

        // Resolve fork if necessary
        tempchain := &Blockchain{Chain: receivedChain}
        tempchain.ResolveFork(receivedChain)
    })

    // setupStreamHandler updates: Add a stream handler for heartbeat
    n.Host.SetStreamHandler("/blockchain/1.0.0/heartbeat", func(s network.Stream) {
        defer s.Close()

        // Read peer ID from stream
        buf := make([]byte, 256)
        _, err := s.Read(buf)
        if err != nil {
            log.Printf("Error reading heartbeat stream: %v", err)
            return
        }

        peerID := s.Conn().RemotePeer()
        n.PeerManager.UpdateLastSeen(peerID)
        log.Printf("Received heartbeat from peer: %s", peerID.String())
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

    for _, peer := range n.Host.Peerstore().Peers() {
        if peer == n.Host.ID() {
            continue
        }

        stream, err := n.Host.NewStream(context.Background(), peer, "/blockchain/1.0.0/transaction")
        if err != nil {
            log.Printf("Failed to open stream to peer %s: %v", peer.String(), err)
            continue
        }
        defer stream.Close()

        _, err = stream.Write(data)
        if err != nil {
            log.Printf("Failed to send transaction to peer %s: %v", peer.String(), err)
        }
    }
}

// ConnectToPeer connects to a given peer
func (n *Node) ConnectToPeer(addr string) error {
    peerInfo, err := peer.AddrInfoFromString(addr)
    if err != nil {
        return fmt.Errorf("invalid peer address: %v", err)
    }

    err = n.Host.Connect(context.Background(), *peerInfo)
    if err != nil {
        return fmt.Errorf("failed to connect to peer: %v", err)
    }

    // Add peer to PeerManager
    n.PeerManager.AddPeer(peerInfo)
    n.PeerManager.UpdateLastSeen(peerInfo.ID)

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

// BroadcastChain sends the entire chain to all peers
func (n *Node) BroadcastChain(blockchain *Blockchain) {
    chainData, err := json.Marshal(blockchain.Chain)
    if err != nil {
        log.Printf("[P2P] Failed to serialize blockchain: %v", err)
        return
    }

    for _, peer := range n.Host.Peerstore().Peers() {
        if peer == n.Host.ID() {
            continue
        }

        stream, err := n.Host.NewStream(context.Background(), peer, "/blockchain/1.0.0/chain")
        if err != nil {
            log.Printf("[P2P] Failed to open stream to peer %s: %v", peer.String(), err)
            continue
        }
        defer stream.Close()

        _, err = stream.Write(chainData)
        if err != nil {
            log.Printf("[P2P] Failed to send blockchain data to peer %s: %v", peer.String(), err)
        }
    }
}

func (n *Node) HeartbeatPeers() {
    for {
        peers := n.PeerManager.GetPeers()
        for _, p := range peers {
            stream, err := n.Host.NewStream(context.Background(), p.ID, "/blockchain/1.0.0/heartbeat")
            if err != nil {
                log.Printf("Peer %s is inactive: %v", p.ID, err)
                n.PeerManager.RemovePeer(p.ID)
                continue
            }
            stream.Close()
        }
        time.Sleep(30 * time.Second) // Periodic check
    }
}

func (n *Node) BroadcastMessageToPeers(topic string, message []byte) {
    peers := n.PeerManager.GetPeers()
    for _, p := range peers {
        protocolID := protocol.ID(topic)
        stream, err := n.Host.NewStream(context.Background(), p.ID, protocolID)
        if err != nil {
            log.Printf("Failed to connect to peer %s: %v", p.ID.String(), err)
            continue
        }
        _, err = stream.Write(message)
        if err != nil {
            log.Printf("Failed to send message to peer %s: %v", p.ID.String(), err)
        }
        stream.Close()
    }
}

// RequestChain sends a request for a blockchain segment.
func (n *Node) RequestChain(start, end int) ([]Block, error) {
    request := fmt.Sprintf("%d,%d", start, end)
    for _, peer := range n.PeerManager.GetPeers() {
        stream, err := n.Host.NewStream(context.Background(), peer.ID, "/blockchain/1.0.0/request_chain")
        if err != nil {
            log.Printf("Failed to open stream to peer %s: %v", peer.ID.String(), err)
            continue
        }
        defer stream.Close()

        _, err = stream.Write([]byte(request))
        if err != nil {
            log.Printf("Failed to send chain request to peer %s: %v", peer.ID.String(), err)
            continue
        }

        buf := make([]byte, 4096)
        bytesRead, err := stream.Read(buf)
        if err != nil {
            log.Printf("Failed to read chain segment from peer %s: %v", peer.ID.String(), err)
            continue
        }

        var chainSegment []Block
        if err := json.Unmarshal(buf[:bytesRead], &chainSegment); err != nil {
            log.Printf("Failed to deserialize chain segment: %v", err)
            return nil, err
        }

        return chainSegment, nil
    }
    return nil, fmt.Errorf("failed to retrieve chain segment from peers")
}

// SendChainSegment responds to a chain request.
func (n *Node) SendChainSegment(stream network.Stream, start, end int) {
    defer stream.Close()

    if start < 0 || end > len(n.Blockchain.Chain) || start >= end {
        log.Printf("Invalid chain segment request: start=%d, end=%d", start, end)
        return
    }

    // Extract the chain segment
    chainSegment := n.Blockchain.Chain[start:end]

    // Serialize the chain segment
    data, err := json.Marshal(chainSegment)
    if err != nil {
        log.Printf("Failed to serialize chain segment: %v", err)
        return
    }

    // Send the serialized data to the stream
    _, err = stream.Write(data)
    if err != nil {
        log.Printf("Failed to send chain segment: %v", err)
    }
}

// ValidateAndUpdateChain validates and integrates a received chain.
func (n *Node) ValidateAndUpdateChain(newChain []Block) bool {
    if n.Blockchain.ValidateCandidateChain(newChain) {
        n.Blockchain.ReplaceChain(newChain)
        log.Printf("Chain updated with received segment. Length: %d", len(newChain))
        return true
    }
    log.Println("Received chain segment is invalid.")
    return false
}

// BroadcastHeartbeat sends a heartbeat to all peers
func (n *Node) BroadcastHeartbeat() {
    for _, p := range n.PeerManager.GetPeers() {
        go func(peerID peer.ID) {
            stream, err := n.Host.NewStream(context.Background(), peerID, "/blockchain/1.0.0/heartbeat")
            if err != nil {
                log.Printf("Failed to open heartbeat stream to peer %s: %v", peerID.String(), err)
                return
            }
            defer stream.Close()

            _, err = stream.Write([]byte(n.Host.ID().String()))
            if err != nil {
                log.Printf("Failed to send heartbeat to peer %s: %v", peerID.String(), err)
            }
        }(p.ID)
    }
}

// PruneInactivePeers removes peers that are no longer active
func (n *Node) PruneInactivePeers(timeout int64) {
    inactivePeers := n.PeerManager.GetInactivePeers(timeout)
    for _, peerID := range inactivePeers {
        n.PeerManager.RemovePeer(peerID)
        log.Printf("Pruned inactive peer: %s", peerID.String())
    }
}

// StartHeartbeatRoutine starts a periodic heartbeat broadcast
func (n *Node) StartHeartbeatRoutine(ctx context.Context, interval, timeout int) {
    ticker := time.NewTicker(time.Duration(interval) * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            // Send heartbeat to all peers
            for _, peerInfo := range n.PeerManager.GetPeers() {
                if err := n.sendHeartbeat(peerInfo.ID); err != nil {
                    log.Printf("Failed to send heartbeat to peer %s: %v", peerInfo.ID, err)
                    n.PeerManager.RemovePeer(peerInfo.ID)
                }
            }

            // Check for inactive peers
            inactivePeers := n.PeerManager.GetInactivePeers(int64(timeout))
            for _, peerID := range inactivePeers {
                n.PeerManager.RemovePeer(peerID)
                log.Printf("Removed inactive peer: %s", peerID)
            }
        }
    }
}

// Add a helper method for sending heartbeats
func (n *Node) sendHeartbeat(peerID peer.ID) error {
    stream, err := n.Host.NewStream(context.Background(), peerID, "/heartbeat/1.0.0")
    if err != nil {
        return err
    }
    defer stream.Close()

    // Send simple heartbeat message
    _, err = stream.Write([]byte("ping"))
    return err
}

// Add this method to Node struct
func (n *Node) SetupStreamHandler() {
    // Set up heartbeat stream handler
    n.Host.SetStreamHandler("/heartbeat/1.0.0", func(stream network.Stream) {
        defer stream.Close()

        // Update peer's last seen timestamp
        peerID := stream.Conn().RemotePeer()
        peerInfo := peer.AddrInfo{
            ID:    peerID,
            Addrs: n.Host.Network().Peerstore().Addrs(peerID),
        }

        // Add or update peer
        n.PeerManager.AddPeer(&peerInfo)
        n.PeerManager.UpdateLastSeen(peerID)

        // Read the heartbeat message
        buf := make([]byte, 4)
        _, err := stream.Read(buf)
        if err != nil {
            log.Printf("Error reading heartbeat: %v", err)
            return
        }
    })
}

func (n *Node) MaintainConnectionPool(ctx context.Context, interval, timeout int) {
    ticker := time.NewTicker(time.Duration(interval) * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            // Prune inactive peers
            n.PruneInactivePeers(int64(timeout))

            // Log connection pool status
            log.Printf("Active connections: %d/%d", n.PeerManager.GetConnectionPoolSize(), MaxConnections)
        }
    }
}

func (n *Node) bootstrapDHT(ctx context.Context) error {
    // Bootstrap the DHT
    if err := n.DHT.Bootstrap(ctx); err != nil {
        return fmt.Errorf("failed to bootstrap DHT: %w", err)
    }

    // Connect to bootstrap peers
    bootstrapPeers := n.DHT.RoutingTable().ListPeers()
    for _, peer := range bootstrapPeers {
        if peer == n.Host.ID() {
            continue
        }
        
        peerInfo := n.Host.Peerstore().PeerInfo(peer)
        if err := n.Host.Connect(ctx, peerInfo); err != nil {
            log.Printf("Failed to connect to bootstrap peer %s: %v", peer, err)
        }
    }

    return nil
}

// VerifyPeerConnection checks if two nodes are connected
func VerifyPeerConnection(node1, node2 *Node) bool {
    peers := node1.Host.Network().Peers()
    for _, peer := range peers {
        if peer.String() == node2.Host.ID().String() {
            return true
        }
    }
    return false
}
