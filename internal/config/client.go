package config

import (
	"io"
	"os"
	"time"

	"sync"
	"sync/atomic"
	"udup/internal"
	ubase "udup/internal/client/driver/mysql/base"
	umconf "udup/internal/config/mysql"
	"udup/internal/models"
)

// This is the default port that we use for Serf communication
const (
	DefaultBindPort  int = 8191
	DefaultClusterID     = "udup-cluster"
)

// RPCHandler can be provided to the Client if there is a local server
// to avoid going over the network. If not provided, the Client will
// maintain a connection pool to the servers
type RPCHandler interface {
	RPC(method string, args interface{}, reply interface{}) error
}

// Config is used to parameterize and configure the behavior of the client
type ClientConfig struct {
	// StateDir is where we store our state
	StateDir string

	// AllocDir is where we store data for allocations
	AllocDir string

	// LogOutput is the destination for logs
	LogOutput io.Writer

	// Specify the log file name. The empty string means to log to stdout.
	LogFile string

	// Region is the clients region
	Region string

	// Servers is a list of known server addresses. These are as "host:port"
	Servers []string

	// RPCHandler can be provided to avoid network traffic if the
	// server is running locally.
	RPCHandler RPCHandler

	// Node provides the base node
	Node *models.Node

	// Version is the version of the Udup client
	Version string

	// Revision is the commit number of the Udup client
	Revision string

	// ConsulConfig is this Agent's Consul configuration
	ConsulConfig *ConsulConfig

	NatsConfig *NatsConfig

	// StatsCollectionInterval is the interval at which the Udup client
	// collects resource usage stats
	StatsCollectionInterval time.Duration

	// PublishNodeMetrics determines whether server is going to publish node
	// level metrics to remote Metric sinks
	PublishNodeMetrics bool

	// PublishAllocationMetrics determines whether server is going to publish
	// allocation metrics to remote Metric sinks
	PublishAllocationMetrics bool

	// LogLevel is the level of the logs to putout
	LogLevel string

	// NoHostUUID disables using the host's UUID and will force generation of a
	// random UUID.
	NoHostUUID bool
}

func (c *ClientConfig) Copy() *ClientConfig {
	nc := new(ClientConfig)
	*nc = *c
	nc.Node = nc.Node.Copy()
	nc.Servers = internal.CopySliceString(nc.Servers)
	nc.ConsulConfig = c.ConsulConfig.Copy()
	return nc
}

type DriverCtx struct {
	DriverConfig *MySQLDriverConfig
}

type NatsConfig struct {
	Addr         string `mapstructure:"address"`
	StoreType    string `mapstructure:"store_type"`
	FilestoreDir string `mapstructure:"file_store_dir"`
}

type CutOver int

const (
	CutOverAtomic  CutOver = iota
	CutOverTwoStep         = iota
)

type MySQLDriverConfig struct {
	//Ref:http://dev.mysql.com/doc/refman/5.7/en/replication-options-slave.html#option_mysqld_replicate-do-table
	ReplicateDoDb                       []*DataSource
	ConcurrentCountTableRows            bool
	SkipRenamedColumns                  bool
	MaxRetries                          int64
	ChunkSize                           int64
	niceRatio                           float64
	MaxLagMillisecondsThrottleThreshold int64
	maxLoad                             umconf.LoadMap
	criticalLoad                        umconf.LoadMap
	PostponeCutOverFlagFile             string
	CutOverLockTimeoutSeconds           int64
	RowsDeltaEstimate                   int64
	TimeZone                            string

	Gtid                     string
	NatsAddr                 string
	ParallelWorkers          int
	ConnectionConfig         *umconf.ConnectionConfig
	HasSuperPrivilege        bool
	BinlogFormat             string
	BinlogRowImage           string
	MySQLVersion             string
	StartTime                time.Time
	RowCopyStartTime         time.Time
	RowCopyEndTime           time.Time
	LockTablesStartTime      time.Time
	RenameTablesStartTime    time.Time
	RenameTablesEndTime      time.Time
	PointOfInterestTime      time.Time
	pointOfInterestTimeMutex *sync.Mutex
	TotalDMLEventsApplied    int64
	TotalRowsCopied          int64

	CutOverType          CutOver
	ApproveHeterogeneous bool

	throttleMutex                          *sync.Mutex
	IsPostponingCutOver                    int64
	CountingRowsFlag                       int64
	AllEventsUpToLockProcessedInjectedFlag int64
	ShutdownFlag                           int64
	UserCommandedUnpostponeFlag            int64
	CutOverCompleteFlag                    int64
	InCutOverCriticalSectionFlag           int64

	Iteration int64

	recentBinlogCoordinates ubase.BinlogCoordinates
}

func (a *MySQLDriverConfig) SetDefault() *MySQLDriverConfig {
	result := *a

	if result.ParallelWorkers == 0 {
		result.ParallelWorkers = 1
	}
	return &result
}

// RequiresBinlogFormatChange is `true` when the original binlog format isn't `ROW`
func (m *MySQLDriverConfig) RequiresBinlogFormatChange() bool {
	return m.BinlogFormat != "ROW"
}

// ElapsedRowCopyTime returns time since starting to copy chunks of rows
func (m *MySQLDriverConfig) MarkRowCopyEndTime() {
	m.throttleMutex.Lock()
	defer m.throttleMutex.Unlock()
	m.RowCopyEndTime = time.Now()
}

// MarkRowCopyStartTime
func (m *MySQLDriverConfig) MarkRowCopyStartTime() {
	m.throttleMutex.Lock()
	defer m.throttleMutex.Unlock()
	m.RowCopyStartTime = time.Now()
}

func (m *MySQLDriverConfig) SetRecentBinlogCoordinates(coordinates ubase.BinlogCoordinates) {
	m.throttleMutex.Lock()
	defer m.throttleMutex.Unlock()
	m.recentBinlogCoordinates = coordinates
}

func (m *MySQLDriverConfig) GetIteration() int64 {
	return atomic.LoadInt64(&m.Iteration)
}

func (m *MySQLDriverConfig) TimeSincePointOfInterest() time.Duration {
	m.pointOfInterestTimeMutex.Lock()
	defer m.pointOfInterestTimeMutex.Unlock()

	return time.Since(m.PointOfInterestTime)
}

// ElapsedRowCopyTime returns time since starting to copy chunks of rows
func (m *MySQLDriverConfig) ElapsedRowCopyTime() time.Duration {
	//m.throttleMutex.Lock()
	//defer m.throttleMutex.Unlock()

	if m.RowCopyStartTime.IsZero() {
		// Row copy hasn't started yet
		return 0
	}

	if m.RowCopyEndTime.IsZero() {
		return time.Since(m.RowCopyStartTime)
	}
	return m.RowCopyEndTime.Sub(m.RowCopyStartTime)
}

// GetTotalRowsCopied returns the accurate number of rows being copied (affected)
// This is not exactly the same as the rows being iterated via chunks, but potentially close enough
func (m *MySQLDriverConfig) GetTotalRowsCopied() int64 {
	return atomic.LoadInt64(&m.TotalRowsCopied)
}

// ElapsedTime returns time since very beginning of the process
func (m *MySQLDriverConfig) ElapsedTime() time.Duration {
	return time.Since(m.StartTime)
}

func (m *MySQLDriverConfig) GetNiceRatio() float64 {
	//m.throttleMutex.Lock()
	//defer m.throttleMutex.Unlock()

	return m.niceRatio
}

func (m *MySQLDriverConfig) GetMaxLoad() umconf.LoadMap {
	//m.throttleMutex.Lock()
	//defer m.throttleMutex.Unlock()

	return m.maxLoad.Duplicate()
}

func (m *MySQLDriverConfig) GetCriticalLoad() umconf.LoadMap {
	//m.throttleMutex.Lock()
	//defer m.throttleMutex.Unlock()

	return m.criticalLoad.Duplicate()
}

func (m *MySQLDriverConfig) MarkPointOfInterest() int64 {
	//m.pointOfInterestTimeMutex.Lock()
	//defer m.pointOfInterestTimeMutex.Unlock()

	m.PointOfInterestTime = time.Now()
	return atomic.LoadInt64(&m.Iteration)
}

// TableName is the table configuration
// slave restrict replication to a given table
type DataSource struct {
	Database string
	Table    []*Table
}

// TableName is the Table configuration
// slave restrict replication to a given table
type TableName struct {
	Database string
	Name     string
}

type Table struct {
	Name string

	AlterStatement                   string
	OriginalTableColumnsOnApplier    *umconf.ColumnList
	OriginalTableColumns             *umconf.ColumnList
	OriginalTableUniqueKeys          [](*umconf.UniqueKey)
	SharedColumns                    *umconf.ColumnList
	ColumnRenameMap                  map[string]string
	DroppedColumnsMap                map[string]bool
	MappedSharedColumns              *umconf.ColumnList
	MigrationRangeMinValues          *umconf.ColumnValues
	MigrationRangeMaxValues          *umconf.ColumnValues
	Iteration                        int64
	MigrationIterationRangeMinValues *umconf.ColumnValues
	MigrationIterationRangeMaxValues *umconf.ColumnValues

	TableEngine  string
	RowsEstimate int64
}

// DefaultConfig returns the default configuration
func DefaultClientConfig() *ClientConfig {
	return &ClientConfig{
		NatsConfig:              DefaultNatsConfig(),
		ConsulConfig:            DefaultConsulConfig(),
		LogOutput:               os.Stderr,
		Region:                  "global",
		StatsCollectionInterval: 1 * time.Second,
		LogLevel:                "DEBUG",
	}
}

// DefaultNatsConfig() returns the canonical defaults for the Udup
// `nats` configuration.
func DefaultNatsConfig() *NatsConfig {
	return &NatsConfig{
		Addr:         "127.0.0.1:8193",
		StoreType:    "memory",
		FilestoreDir: "",
	}
}

// Merge merges two Consul Configurations together.
func (a *NatsConfig) Merge(b *NatsConfig) *NatsConfig {
	result := *a

	if b.Addr != "" {
		result.Addr = b.Addr
	}
	if b.StoreType != "" {
		result.StoreType = b.StoreType
	}
	if b.FilestoreDir != "" {
		result.FilestoreDir = b.FilestoreDir
	}
	return &result
}