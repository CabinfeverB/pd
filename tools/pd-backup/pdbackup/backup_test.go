package pdbackup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	sc "github.com/tikv/pd/pkg/schedule/config"
	"github.com/tikv/pd/pkg/storage/endpoint"
	"github.com/tikv/pd/pkg/utils/etcdutil"
	"github.com/tikv/pd/pkg/utils/testutil"
	"github.com/tikv/pd/pkg/utils/typeutil"
	"github.com/tikv/pd/server/config"
	"go.etcd.io/etcd/clientv3"
	"go.etcd.io/etcd/embed"
	"go.uber.org/goleak"
)

var (
	clusterID         = (uint64(1257894000) << 32) + uint64(rand.Uint32())
	allocIDMax        = uint64(100000000)
	allocTimestampMax = uint64(1257894000)
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m, testutil.LeakOptions...)
}

type backupTestSuite struct {
	suite.Suite

	etcd       *embed.Etcd
	etcdClient *clientv3.Client

	server       *httptest.Server
	serverConfig *config.Config
}

func TestBackupTestSuite(t *testing.T) {
	servers, etcdClient, clean := etcdutil.NewTestEtcdCluster(t, 1)
	defer clean()

	server, serverConfig := setupServer()
	testSuite := &backupTestSuite{
		etcd:         servers[0],
		etcdClient:   etcdClient,
		server:       server,
		serverConfig: serverConfig,
	}

	suite.Run(t, testSuite)
}

func setupServer() (*httptest.Server, *config.Config) {
	serverConfig := &config.Config{
		ClientUrls:          "example.com:2379",
		PeerUrls:            "example.com:20480",
		AdvertiseClientUrls: "example.com:2380",
		AdvertisePeerUrls:   "example.com:2380",
		Name:                "test-svc",
		DataDir:             "/data",
		ForceNewCluster:     true,
		EnableGRPCGateway:   true,
		InitialCluster:      "pd1=http://127.0.0.1:10208",
		InitialClusterState: "new",
		InitialClusterToken: "test-token",
		LeaderLease:         int64(1),
		Replication: sc.ReplicationConfig{
			LocationLabels: typeutil.StringSlice{},
		},
		PDServerCfg: config.PDServerConfig{
			RuntimeServices: typeutil.StringSlice{},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		b, err := json.Marshal(serverConfig)
		if err != nil {
			res.WriteHeader(http.StatusInternalServerError)
			res.Write([]byte(fmt.Sprintf("failed setting up test server: %s", err)))
			return
		}

		res.WriteHeader(http.StatusOK)
		res.Write(b)
	}))

	return server, serverConfig
}

func (s *backupTestSuite) BeforeTest(suiteName, testName string) {
	re := s.Require()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()

	_, err := s.etcdClient.Put(
		ctx,
		pdClusterIDPath,
		string(typeutil.Uint64ToBytes(clusterID)))
	re.NoError(err)

	var (
		rootPath               = path.Join(pdRootPath, strconv.FormatUint(clusterID, 10))
		allocTimestampMaxBytes = typeutil.Uint64ToBytes(allocTimestampMax)
	)
	_, err = s.etcdClient.Put(ctx, endpoint.TimestampPath(rootPath), string(allocTimestampMaxBytes))
	re.NoError(err)

	var (
		allocIDPath     = path.Join(rootPath, "alloc_id")
		allocIDMaxBytes = typeutil.Uint64ToBytes(allocIDMax)
	)
	_, err = s.etcdClient.Put(ctx, allocIDPath, string(allocIDMaxBytes))
	re.NoError(err)
}

func (s *backupTestSuite) AfterTest(suiteName, testName string) {
	s.etcd.Close()
}

func (s *backupTestSuite) TestGetBackupInfo() {
	re := s.Require()
	actual, err := GetBackupInfo(s.etcdClient, s.server.URL)
	re.NoError(err)

	expected := &BackupInfo{
		ClusterID:         clusterID,
		AllocIDMax:        allocIDMax,
		AllocTimestampMax: allocTimestampMax,
		Config:            s.serverConfig,
	}
	re.Equal(expected, actual)

	tmpFile, err := os.CreateTemp(os.TempDir(), "pd_backup_info_test.json")
	re.NoError(err)
	defer os.RemoveAll(tmpFile.Name())

	re.NoError(OutputToFile(actual, tmpFile))
	_, err = tmpFile.Seek(0, 0)
	re.NoError(err)

	b, err := io.ReadAll(tmpFile)
	re.NoError(err)

	var restored BackupInfo
	err = json.Unmarshal(b, &restored)
	re.NoError(err)

	re.Equal(expected, &restored)
}
