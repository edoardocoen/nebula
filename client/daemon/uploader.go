package daemon

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/samoslab/nebula/client/config"
	client "github.com/samoslab/nebula/client/provider_client"
	pb "github.com/samoslab/nebula/provider/pb"
	mpb "github.com/samoslab/nebula/tracker/metadata/pb"
	util_hash "github.com/samoslab/nebula/util/hash"
	"github.com/sirupsen/logrus"

	"google.golang.org/grpc"
)

var (
	// ReplicaFileSize using replication if file size less than
	ReplicaFileSize  = int64(8 * 1024)
	PartitionMaxSize = int64(256 * 1024 * 1024)
)

// ClientManager client manager
type ClientManager struct {
	mclient    mpb.MatadataServiceClient
	NodeId     []byte
	TempDir    string
	log        *logrus.Logger
	cfg        *config.ClientConfig
	serverConn *grpc.ClientConn
}

// NewClientManager create manager
func NewClientManager(log *logrus.Logger, trackerServer string, cfg *config.ClientConfig) (*ClientManager, error) {
	if trackerServer == "" {
		return nil, errors.New("tracker server nil")
	}
	if cfg == nil {
		return nil, errors.New("client config nil")
	}
	c := &ClientManager{}
	conn, err := grpc.Dial(trackerServer, grpc.WithInsecure())
	if err != nil {
		fmt.Printf("RPC Dial failed: %s", err.Error())
		return nil, err
	}
	fmt.Printf("tracker server %s\n", trackerServer)
	//defer conn.Close()
	c.serverConn = conn

	c.mclient = mpb.NewMatadataServiceClient(conn)
	c.log = log
	c.TempDir = cfg.TempDir
	c.NodeId = cfg.Node.NodeId
	c.cfg = cfg
	return c, nil
}

// Shutdown shutdown tracker connection
func (c *ClientManager) Shutdown() {
	c.serverConn.Close()
}

// PingProvider ping provider
func (c *ClientManager) PingProvider(pro []*mpb.BlockProviderAuth) ([]*mpb.BlockProviderAuth, error) {
	return pro, nil
}

func (c *ClientManager) ConnectProvider() error {
	return nil
}

// UploadFile upload file to provider
func (c *ClientManager) UploadFile(filename string) error {
	log := c.log
	req, rsp, err := c.CheckFileExists(filename)
	if err != nil {
		return err
	}

	log.Infof("check file exists rsp:%+v\n", rsp)
	if rsp.GetCode() == 0 {
		log.Infof("upload success %s %s", filename, rsp.GetErrMsg())
		return nil
	}
	// 1 can upload
	if rsp.GetCode() != 1 {
		return fmt.Errorf("%d:%s", rsp.GetCode(), rsp.GetErrMsg())
	}

	log.Infof("start upload file %s", filename)
	switch rsp.GetStoreType() {
	case mpb.FileStoreType_MultiReplica:
		log.Infof("upload manner is multi-replication\n")
		partitions, err := c.uploadFileByMultiReplica(req, rsp)
		if err != nil {
			return err
		}
		return c.UploadFileDone(req, partitions)
	case mpb.FileStoreType_ErasureCode:
		log.Infof("upload manner erasure\n")
		partFiles := []string{}
		var err error
		fileSize := int64(req.GetFileSize())
		if fileSize > PartitionMaxSize {
			chunkNum := int(math.Ceil(float64(fileSize) / float64(PartitionMaxSize)))
			chunkSize := fileSize / int64(chunkNum)
			partFiles, err = FileSplit(c.TempDir, filename, chunkSize)
			if err != nil {
				return err
			}
		} else {
			partFiles = append(partFiles, filename)
		}

		fileInfos := []MyPart{}

		for _, fname := range partFiles {
			fileSlices, err := c.OnlyFileSplit(fname, int(rsp.GetDataPieceCount()), int(rsp.GetVerifyPieceCount()))
			if err != nil {
				return err
			}
			fileInfos = append(fileInfos, MyPart{Filename: fname, Pieces: fileSlices})
		}
		fmt.Printf("fileinfos %+v\n", fileInfos)

		ufpr := &mpb.UploadFilePrepareReq{}
		ufpr.Version = 1
		ufpr.FileHash = req.FileHash
		ufpr.Timestamp = uint64(time.Now().UTC().Unix())
		ufpr.NodeId = req.NodeId
		ufpr.FileSize = req.FileSize
		ufpr.Partition = make([]*mpb.SplitPartition, len(partFiles))
		// todo delete temp file
		for i, partInfo := range fileInfos {
			phslist := []*mpb.PieceHashAndSize{}
			for _, slice := range partInfo.Pieces {
				phs := &mpb.PieceHashAndSize{}
				phs.Hash = slice.FileHash
				phs.Size = uint32(slice.FileSize)
				phslist = append(phslist, phs)
			}
			ufpr.Partition[i] = &mpb.SplitPartition{phslist}
		}
		err = ufpr.SignReq(c.cfg.Node.PriKey)
		if err != nil {
			return err
		}

		ctx := context.Background()
		log.Infof("upload file prepare req:%x", ufpr.FileHash)
		ufprsp, err := c.mclient.UploadFilePrepare(ctx, ufpr)
		if err != nil {
			log.Errorf("UploadFilePrepare error %v", err)
			return err
		}

		rspPartitions := ufprsp.GetPartition()
		log.Infof("upload prepare response partitions count:%d", len(rspPartitions))

		if len(rspPartitions) == 0 {
			return fmt.Errorf("only 0 partitions")
		}

		for _, part := range rspPartitions {
			auth := part.GetProviderAuth()
			for _, pa := range auth {
				fmt.Printf("server:%s\n", pa.GetServer())
				fmt.Printf("port:%d\n", pa.GetPort())
				fmt.Printf("auth:%+v\n", pa.GetHashAuth()[0].GetTicket())
			}
		}

		partitions := []*mpb.StorePartition{}
		for _, partInfo := range fileInfos {

			partition, err := c.uploadFileBatchByErasure(ufpr, rspPartitions, partInfo.Pieces)
			if err != nil {
				return err
			}
			partitions = append(partitions, partition)
		}
		log.Info("upload file done")

		return c.UploadFileDone(req, partitions)

	}
	return nil
}

func (c *ClientManager) CheckFileExists(filename string) (*mpb.CheckFileExistReq, *mpb.CheckFileExistResp, error) {
	log := c.log
	hash, err := util_hash.Sha1File(filename)
	if err != nil {
		return nil, nil, err
	}
	fileInfo, err := os.Stat(filename)
	if err != nil {
		return nil, nil, err
	}
	dir, _ := filepath.Split(filename)
	ctx := context.Background()
	req := &mpb.CheckFileExistReq{}
	req.FileSize = uint64(fileInfo.Size())
	req.Interactive = true
	req.NewVersion = false
	req.Parent = &mpb.FilePath{&mpb.FilePath_Path{dir}}
	req.FileHash = hash
	req.NodeId = c.NodeId
	req.FileName = filename
	req.Timestamp = uint64(time.Now().UTC().Unix())
	mtime, err := GetFileModTime(filename)
	if err != nil {
		return nil, nil, err
	}
	req.FileModTime = uint64(mtime)
	if fileInfo.Size() < ReplicaFileSize {
		fileData, err := util_hash.GetFileData(filename)
		if err != nil {
			log.Errorf("get file %s data error %v", filename, err)
			return nil, nil, err
		}
		req.FileData = fileData
	}
	err = req.SignReq(c.cfg.Node.PriKey)
	if err != nil {
		return nil, nil, err
	}
	log.Infof("checkfileexist req:%x\n", req.GetFileHash())
	rsp, err := c.mclient.CheckFileExist(ctx, req)
	return req, rsp, err
}

// MkFolder create folder
func (c *ClientManager) MkFolder(filepath string, folders []string) (bool, error) {
	log := c.log
	ctx := context.Background()
	req := &mpb.MkFolderReq{}
	req.Parent = &mpb.FilePath{&mpb.FilePath_Path{filepath}}
	req.Folder = folders
	req.NodeId = c.NodeId
	req.Interactive = false
	req.Timestamp = uint64(time.Now().UTC().Unix())
	err := req.SignReq(c.cfg.Node.PriKey)
	if err != nil {
		return false, err
	}
	log.Infof("make folder req:%+v", req.GetFolder())
	rsp, err := c.mclient.MkFolder(ctx, req)
	if rsp.GetCode() != 0 {
		return false, fmt.Errorf("%s", rsp.GetErrMsg())
	}
	log.Infof("make folder response:%+v", rsp)
	return true, nil
}

func (c *ClientManager) OnlyFileSplit(filename string, dataNum, verifyNum int) ([]HashFile, error) {
	// Split file and hash
	// todo delete temp file
	fileSlices, err := RsEncoder(c.TempDir, filename, dataNum, verifyNum)
	if err != nil {
		c.log.Errorf("reed se error %v", err)
		return nil, err
	}
	return fileSlices, nil

}

func (c *ClientManager) uploadFileBatchByErasure(req *mpb.UploadFilePrepareReq, rspPartitions []*mpb.ErasureCodePartition, hashFiles []HashFile) (*mpb.StorePartition, error) {
	log := c.log
	partition := &mpb.StorePartition{}
	partition.Block = []*mpb.StoreBlock{}
	rspPartition := rspPartitions[0]
	phas := rspPartition.GetProviderAuth()
	providers, err := c.PingProvider(phas)
	if err != nil {
		return nil, err
	}

	for i, pro := range providers {
		log.Infof("provider %d %s:%d", i, pro.GetServer(), pro.GetPort())
		if i == 0 {
			block, err := c.uploadFileToErasureProvider(pro, rspPartition.GetTimestamp(), hashFiles[i], true)
			if err != nil {
				return nil, err
			}
			partition.Block = append(partition.Block, block)
		} else {
			block, err := c.uploadFileToErasureProvider(pro, rspPartition.GetTimestamp(), hashFiles[i], false)
			if err != nil {
				return nil, err
			}
			partition.Block = append(partition.Block, block)
		}
	}
	return partition, nil
}

func getOneOfPartition(pro *mpb.ErasureCodePartition) *mpb.BlockProviderAuth {
	pa := pro.GetProviderAuth()[0]
	return pa
}

func (c *ClientManager) uploadFileToErasureProvider(pro *mpb.BlockProviderAuth, tm uint64, fileInfo HashFile, first bool) (*mpb.StoreBlock, error) {
	log := c.log
	block := &mpb.StoreBlock{}
	onePartition := pro
	server := fmt.Sprintf("%s:%d", onePartition.GetServer(), onePartition.GetPort())
	log.Infof("file %+v upload to server %s", fileInfo, server)
	conn, err := grpc.Dial(server, grpc.WithInsecure())
	if err != nil {
		fmt.Printf("RPC Dial failed: %s", err.Error())
		return nil, err
	}
	defer conn.Close()
	pclient := pb.NewProviderServiceClient(conn)

	ha := onePartition.GetHashAuth()[0]
	err = client.StorePiece(pclient, fileInfo.FileName, ha.GetAuth(), ha.GetTicket(), tm, fileInfo.FileHash, uint64(fileInfo.FileSize), first)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}
	block.Hash = fileInfo.FileHash
	block.Size = uint64(fileInfo.FileSize)
	block.BlockSeq = uint32(fileInfo.SliceIndex)
	block.Checksum = true
	block.StoreNodeId = [][]byte{}
	block.StoreNodeId = append(block.StoreNodeId, []byte(onePartition.GetNodeId()))

	return block, nil
}

func (c *ClientManager) uploadFileToReplicaProvider(pro *mpb.ReplicaProvider, fileInfo HashFile) ([]byte, error) {
	log := c.log
	server := fmt.Sprintf("%s:%d", pro.GetServer(), pro.GetPort())
	log.Infof("upload to provider %s\n", server)
	conn, err := grpc.Dial(server, grpc.WithInsecure())
	if err != nil {
		log.Errorf("RPC Dail failed: %v", err)
		return nil, err
	}
	defer conn.Close()
	pclient := pb.NewProviderServiceClient(conn)
	log.Debugf("upload fileinfo %+v", fileInfo)
	log.Debugf("provider auth %x", pro.GetAuth())
	log.Debugf("provider ticket %+v", pro.GetTicket())
	log.Debugf("provider nodeid %x", pro.GetNodeId())
	log.Debugf("provider time %d", pro.GetTimestamp())

	err = client.StorePiece(pclient, fileInfo.FileName, pro.GetAuth(), pro.GetTicket(), pro.GetTimestamp(), fileInfo.FileHash, uint64(fileInfo.FileSize), true)
	if err != nil {
		log.Errorf("upload error %v\n", err)
		return nil, err
	}

	log.Infof("upload file %s success", fileInfo.FileName)

	return pro.GetNodeId(), nil
}

func (c *ClientManager) uploadFileByMultiReplica(req *mpb.CheckFileExistReq, rsp *mpb.CheckFileExistResp) ([]*mpb.StorePartition, error) {

	fileInfo := HashFile{}
	fileInfo.FileName = req.FileName
	fileInfo.FileSize = int64(req.FileSize)
	fileInfo.FileHash = req.FileHash
	fileInfo.SliceIndex = 0

	block := &mpb.StoreBlock{}
	block.Hash = fileInfo.FileHash
	block.Size = uint64(fileInfo.FileSize)
	block.BlockSeq = uint32(fileInfo.SliceIndex)
	block.Checksum = false
	block.StoreNodeId = [][]byte{}
	for _, pro := range rsp.GetProvider() {
		proID, err := c.uploadFileToReplicaProvider(pro, fileInfo)
		if err != nil {
			return nil, err
		}
		block.StoreNodeId = append(block.StoreNodeId, proID)
	}

	partition := &mpb.StorePartition{}
	partition.Block = append(partition.Block, block)
	partitions := []*mpb.StorePartition{partition}
	fmt.Printf("partitions %+v\n", partitions)
	return partitions, nil
}

func (c *ClientManager) UploadFileDone(reqCheck *mpb.CheckFileExistReq, partitions []*mpb.StorePartition) error {
	req := &mpb.UploadFileDoneReq{}
	req.Version = 1
	req.NodeId = c.NodeId
	req.FileHash = reqCheck.GetFileHash()
	req.FileSize = reqCheck.GetFileSize()
	req.FileName = reqCheck.GetFileName()
	req.FileModTime = reqCheck.GetFileModTime()

	req.Parent = reqCheck.GetParent()
	req.Interactive = reqCheck.GetInteractive()
	req.NewVersion = reqCheck.GetNewVersion()

	req.Timestamp = uint64(time.Now().UTC().Unix())
	req.Partition = partitions
	err := req.SignReq(c.cfg.Node.PriKey)
	if err != nil {
		return err
	}
	ctx := context.Background()
	c.log.Infof("upload file done req:%x", req.GetFileHash())
	ufdrsp, err := c.mclient.UploadFileDone(ctx, req)
	if err != nil {
		return err
	}
	c.log.Infof("upload done code: %d", ufdrsp.GetCode())
	if ufdrsp.GetCode() != 0 {
		return fmt.Errorf("%s", ufdrsp.GetErrMsg())
	}
	return nil
}

func (c *ClientManager) ListFiles(path string) ([]*DownFile, error) {
	req := &mpb.ListFilesReq{}
	req.Version = 1
	req.Timestamp = uint64(time.Now().UTC().Unix())
	req.NodeId = c.NodeId
	req.PageSize = 10
	req.PageNum = 1
	req.SortType = mpb.SortType_Name
	req.Parent = &mpb.FilePath{&mpb.FilePath_Path{path}}
	req.AscOrder = true
	err := req.SignReq(c.cfg.Node.PriKey)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	c.log.Infof("req:%+v", req.Timestamp)
	rsp, err := c.mclient.ListFiles(ctx, req)

	if err != nil {
		return nil, err
	}

	if rsp.GetCode() != 0 {
		return nil, fmt.Errorf("errmsg %s", rsp.GetErrMsg())
	}
	fileLists := []*DownFile{}
	for _, info := range rsp.GetFof() {
		hash := hex.EncodeToString(info.GetFileHash())
		id := string(info.GetId())
		df := &DownFile{ID: id, FileName: info.GetName(), Folder: info.GetFolder(), FileHash: hash, FileSize: info.GetFileSize()}
		fileLists = append(fileLists, df)
	}
	return fileLists, nil
}

// DownloadFile download file
func (c *ClientManager) DownloadFile(fileName string, filehash string, fileSize uint64, folder bool) error {
	log := c.log
	fileHash, err := hex.DecodeString(filehash)
	if err != nil {
		return err
	}
	fof := &mpb.FileOrFolder{Name: fileName, FileHash: fileHash, FileSize: fileSize, Folder: folder}
	if fof.GetFolder() {
		log.Infof("download folder %s", fof.GetName())
	}
	req := &mpb.RetrieveFileReq{}
	req.Version = 1
	req.NodeId = c.NodeId
	req.Timestamp = uint64(time.Now().UTC().Unix())
	req.FileHash = fof.FileHash
	req.FileSize = fof.FileSize
	err = req.SignReq(c.cfg.Node.PriKey)
	if err != nil {
		return err
	}
	ctx := context.Background()
	log.Infof("download file req size:%d, hash:%+v", fof.GetFileSize(), fof.GetFileHash())
	rsp, err := c.mclient.RetrieveFile(ctx, req)
	if err != nil {
		return err
	}
	if rsp.GetCode() != 0 {
		return fmt.Errorf("%s", rsp.GetErrMsg())
	}

	// tiny file
	if filedata := rsp.GetFileData(); filedata != nil {
		saveFile(fof.Name, filedata)
		log.Infof("tiny file %s", fof.Name)
		return nil
	}

	partitions := rsp.GetPartition()
	log.Infof("there is %d partitions", len(partitions))
	partitionCount := len(partitions)
	if partitionCount == 1 {
		blockCount := len(partitions[0].GetBlock())
		if blockCount == 1 { // 1 partition 1 block is multiReplica
			log.Infof("file %s is multi replication files", fof.Name)
			_, _, err := c.saveFileByPartition(fof.Name, partitions[0], true)
			return err
		}
	}

	// erasure files
	datas := 0
	paritys := 0
	for _, partition := range partitions {
		datas, paritys, err = c.saveFileByPartition(fof.Name, partition, false)
		if err != nil {
			log.Errorf("save file by partition error %v", err)
			return err
		}
		log.Infof("dataShards %d, parityShards %d", datas, paritys)
	}

	log.Infof("file %s erasure files", fof.Name)
	err = RsDecoder(fof.Name, "", datas, paritys)
	if err != nil {
		return err
	}

	return nil
}

func (c *ClientManager) saveFileByPartition(filename string, partition *mpb.RetrievePartition, multiReplica bool) (int, int, error) {
	log := c.log
	log.Infof("there is %d blocks", len(partition.GetBlock()))
	dataShards := 0
	parityShards := 0
	for _, block := range partition.GetBlock() {
		if block.GetChecksum() {
			dataShards++
		} else {
			parityShards++
		}
		node := block.GetStoreNode()
		node1 := node[0]
		server := fmt.Sprintf("%s:%d", node1.GetServer(), node1.GetPort())
		conn, err := grpc.Dial(server, grpc.WithInsecure())
		if err != nil {
			log.Errorf("RPC Dial failed: %s", err.Error())
			return 0, 0, err
		}
		defer conn.Close()
		pclient := pb.NewProviderServiceClient(conn)

		tempFileName := fmt.Sprintf("%s.%d", filename, block.GetBlockSeq())
		if multiReplica {
			tempFileName = filename
		}
		log.Infof("file %s store node is %s", tempFileName, server)
		log.Infof("node ticket %s", node1.GetTicket())
		log.Infof("filename %s ticket %s size %d", tempFileName, node1.GetTicket(), block.GetSize())
		err = client.Retrieve(pclient, tempFileName, node1.GetAuth(), node1.GetTicket(), block.GetHash(), block.GetSize())
		if err != nil {
			return 0, 0, err
		}
		log.Infof("retrieve %s success", tempFileName)
	}

	// rs code
	return dataShards, parityShards, nil
}

func saveFile(fileName string, content []byte) error {
	// open output file
	fo, err := os.Create(fileName)
	if err != nil {
		return err
	}
	defer fo.Close()
	if _, err := fo.Write(content); err != nil {
		return err
	}
	return nil
}

// RemoveFile download file
func (c *ClientManager) RemoveFile(filePath string, recursive bool, isPath bool) error {
	log := c.log
	req := &mpb.RemoveReq{}
	req.NodeId = c.NodeId
	req.Timestamp = uint64(time.Now().Unix())
	req.Recursive = recursive
	if isPath {
		req.Target = &mpb.FilePath{&mpb.FilePath_Path{filePath}}
	} else {
		log.Infof("delete file by id %s", filePath)
		req.Target = &mpb.FilePath{&mpb.FilePath_Id{[]byte(filePath)}}
	}

	err := req.SignReq(c.cfg.Node.PriKey)
	if err != nil {
		return err
	}

	log.Infof("remove file req:%s", filePath)
	rsp, err := c.mclient.Remove(context.Background(), req)
	if err != nil {
		return err
	}
	log.Infof("remove file rsp:%+v", rsp)
	if rsp.GetCode() != 0 {
		return fmt.Errorf("%s", rsp.GetErrMsg())
	}
	return nil

}
