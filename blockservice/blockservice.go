// package blockservice implements a BlockService interface that provides
// a single GetBlock/AddBlock interface that seamlessly retrieves data either
// locally or from a remote peer through the exchange.
package blockservice

import (
	"errors"
	"fmt"

	blocks "github.com/ipfs/go-blocks"
	exchange "github.com/ipfs/go-blocks/blockservice/exchange"
	worker "github.com/ipfs/go-blocks/blockservice/worker"
	blockstore "github.com/ipfs/go-blocks/blockstore"
	key "github.com/ipfs/go-blocks/key"

	context "github.com/ipfs/go-blocks/Godeps/_workspace/src/golang.org/x/net/context"
)

var wc = worker.Config{
	// When running on a single core, NumWorkers has a harsh negative effect on
	// throughput. (-80% when < 25)
	// Running a lot more workers appears to have very little effect on both
	// single and multicore configurations.
	NumWorkers: 25,

	// These have no effect on when running on multiple cores, but harsh
	// negative effect on throughput when running on a single core
	// On multicore configurations these buffers have little effect on
	// throughput.
	// On single core configurations, larger buffers have severe adverse
	// effects on throughput.
	ClientBufferSize: 0,
	WorkerBufferSize: 0,
}

var ErrNotFound = errors.New("blockservice: key not found")

// BlockService is a hybrid block datastore. It stores data in a local
// datastore and may retrieve data from a remote Exchange.
// It uses an internal `datastore.Datastore` instance to store values.
type BlockService struct {
	// TODO don't expose underlying impl details
	Blockstore blockstore.Blockstore
	Exchange   exchange.Interface

	worker *worker.Worker
}

// NewBlockService creates a BlockService with given datastore instance.
func New(bs blockstore.Blockstore, rem exchange.Interface) (*BlockService, error) {
	if bs == nil {
		return nil, fmt.Errorf("BlockService requires valid blockstore")
	}

	return &BlockService{
		Blockstore: bs,
		Exchange:   rem,
		worker:     worker.NewWorker(rem, wc),
	}, nil
}

// AddBlock adds a particular block to the service, Putting it into the datastore.
// TODO pass a context into this if the remote.HasBlock is going to remain here.
func (s *BlockService) AddBlock(b *blocks.Block) (key.Key, error) {
	k := b.Key()
	err := s.Blockstore.Put(b)
	if err != nil {
		return k, err
	}
	if err := s.worker.HasBlock(b); err != nil {
		return "", errors.New("blockservice is closed")
	}
	return k, nil
}

// GetBlock retrieves a particular block from the service,
// Getting it from the datastore using the key (hash).
func (s *BlockService) GetBlock(ctx context.Context, k key.Key) (*blocks.Block, error) {
	block, err := s.Blockstore.Get(k)
	if err == nil {
		return block, nil
		// TODO be careful checking ErrNotFound. If the underlying
		// implementation changes, this will break.
	} else if err == blockstore.ErrNotFound && s.Exchange != nil {
		blk, err := s.Exchange.GetBlock(ctx, k)
		if err != nil {
			return nil, err
		}
		return blk, nil
	} else {
		return nil, ErrNotFound
	}
}

// GetBlocks gets a list of blocks asynchronously and returns through
// the returned channel.
// NB: No guarantees are made about order.
func (s *BlockService) GetBlocks(ctx context.Context, ks []key.Key) <-chan *blocks.Block {
	out := make(chan *blocks.Block, 0)
	go func() {
		defer close(out)
		var misses []key.Key
		for _, k := range ks {
			hit, err := s.Blockstore.Get(k)
			if err != nil {
				misses = append(misses, k)
				continue
			}
			select {
			case out <- hit:
			case <-ctx.Done():
				return
			}
		}

		rblocks, err := s.Exchange.GetBlocks(ctx, misses)
		if err != nil {
			// blocks not found are ignored. this is an optimistic call.
			return
		}

		for b := range rblocks {
			select {
			case out <- b:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// DeleteBlock deletes a block in the blockservice from the datastore
func (s *BlockService) DeleteBlock(k key.Key) error {
	return s.Blockstore.DeleteBlock(k)
}

func (s *BlockService) Close() error {
	return s.worker.Close()
}
