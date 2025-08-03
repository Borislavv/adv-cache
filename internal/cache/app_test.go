package cache

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/repository"
	"github.com/Borislavv/advanced-cache/pkg/storage"
	"github.com/Borislavv/advanced-cache/pkg/storage/lru"
	"path/filepath"
	"testing"
	"time"

	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/k8s/probe/liveness"
	"github.com/stretchr/testify/assert"
)

const TestConfigPath = "advancedCache.cfg.test.yaml"

func TestLoadData_NonInteractive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	//  yeah, I know :( that it '"..", ".."' is just a piece of shit
	cfg, err := config.LoadConfig(filepath.Join("..", "..", TestConfigPath))
	if err != nil {
		t.Fatal(err)
	}

	// Test app building
	app, err := NewApp(ctx, cfg, liveness.NewProbe(time.Second))
	assert.NoError(t, err)

	// Whole load data
	assert.NoError(t, app.LoadData(context.Background(), false))

	// Load data: partially
	backend := repository.NewBackend(ctx, cfg)
	db := lru.NewStorage(ctx, cfg, backend)
	dumper := storage.NewDumper(cfg, db, backend)

	// Test dumping and restoring
	assert.NoError(t, dumper.LoadVersion(ctx, "v4"))
	assert.NoError(t, dumper.Dump(ctx))

	lenBefore := db.RealLen()
	storage.LoadMocks(ctx, cfg, backend, db, 10000)
	lenAfter := db.RealLen()

	if lenBefore == lenAfter {
		t.Error("Mocks was not loaded due to lenBefore == lenAfter suddenly", lenBefore, lenAfter)
	}
}
