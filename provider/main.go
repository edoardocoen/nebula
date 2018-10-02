package main

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/eiannone/keyboard"
	"github.com/robfig/cron"
	collector "github.com/samoslab/nebula/provider/collector_client"
	"github.com/samoslab/nebula/provider/config"
	"github.com/samoslab/nebula/provider/disk"
	"github.com/samoslab/nebula/provider/impl"
	"github.com/samoslab/nebula/provider/node"
	pb "github.com/samoslab/nebula/provider/pb"
	client "github.com/samoslab/nebula/provider/register_client"
	trp_pb "github.com/samoslab/nebula/tracker/register/provider/pb"
	util_hash "github.com/samoslab/nebula/util/hash"
	util_rsa "github.com/samoslab/nebula/util/rsa"
	upnp "github.com/samoslab/nebula/util/upnp"
	"github.com/skycoin/skycoin/src/cipher"
	"github.com/yanzay/log"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

const home_config_folder = ".samos-nebula-provider"

func main() {
	var defaultConfigDirFlag string
	usr, err := user.Current()
	if err != nil {
		fmt.Println("Get OS current user failed: ", err.Error())
		fmt.Println("Be sure to use the -configDir parameter because of this system limitations.")
		defaultConfigDirFlag, err = filepath.Abs(filepath.Dir(os.Args[0]))
		if err != nil {
			fmt.Printf("Get path of %s error: %s\n", os.Args[0], err)
			os.Exit(100)
		}
	} else {
		defaultConfigDirFlag = usr.HomeDir + string(os.PathSeparator) + home_config_folder
	}

	daemonCommand := flag.NewFlagSet("daemon", flag.ExitOnError)
	daemonConfigDirFlag := daemonCommand.String("configDir", defaultConfigDirFlag, "config directory")
	daemonTrackerServerFlag := daemonCommand.String("trackerServer", "tracker.store.samos.io:6677", "tracker server address, eg: tracker.store.samos.io:6677")
	daemonCollectorServerFlag := daemonCommand.String("collectorServer", "collector.store.samos.io:6688", "collector server address, eg: collector.store.samos.io:6688")
	daemonTaskServerFlag := daemonCommand.String("taskServer", "task.store.samos.io:6622", "task server address, eg: task.store.samos.io:6622")
	listenFlag := daemonCommand.String("listen", ":6666", "listen address and port, eg: 111.111.111.111:6666 or :6666")
	disableAutoRefreshIpFlag := daemonCommand.Bool("disableAutoRefreshIp", false, "disable auto refresh provider ip or enable auto refresh provider ip")
	quietFlag := daemonCommand.Bool("quiet", false, "not print dot when running")

	registerCommand := flag.NewFlagSet("register", flag.ExitOnError)
	registerConfigDirFlag := registerCommand.String("configDir", defaultConfigDirFlag, "config directory")
	registerTrackerServerFlag := registerCommand.String("trackerServer", "tracker.store.samos.io:6677", "tracker server address, eg: tracker.store.samos.io:6677")
	registerListenFlag := registerCommand.String("listen", ":6666", "listen address and port, eg: 111.111.111.111:6666 or :6666")
	walletAddressFlag := registerCommand.String("walletAddress", "", "Samos wallet address to accept earnings")
	billEmailFlag := registerCommand.String("billEmail", "", "email where send bill to")
	availabilityFlag := registerCommand.String("availability", "", "promise availability, must equal or more than 97%, eg: 98%, 99%, 99.9%")
	upBandwidthFlag := registerCommand.Uint("upBandwidth", 0, "upload bandwidth, unit: Mbps, eg: 100, 20, 8, 4")
	downBandwidthFlag := registerCommand.Uint("downBandwidth", 0, "download bandwidth, unit: Mbps, eg: 100, 20")
	mainStoragePathFlag := registerCommand.String("mainStoragePath", "", "main storage path")
	mainStorageVolumeFlag := registerCommand.String("mainStorageVolume", "", "main storage volume size, unit TB or GB, eg: 2TB or 500GB")
	extraStorageFlag := registerCommand.String("extraStorage", "", "extra storage, format:path1:volume1,path2:volume2, path can not contain comma, eg: /mnt/sde1:1TB,/mnt/sdf1:800GB,/mnt/sdg1:500GB")
	portFlag := registerCommand.Uint("port", 6666, "outer network port for client to connect, eg:6666")
	hostFlag := registerCommand.String("host", "", "outer ip or domain for client to connect, eg: 123.123.123.123")
	dynamicDomainFlag := registerCommand.String("dynamicDomain", "", "dynamic domain for client to connect, eg: mydomain.xicp.net")

	verifyEmailCommand := flag.NewFlagSet("verifyEmail", flag.ExitOnError)
	verifyEmailConfigDirFlag := verifyEmailCommand.String("configDir", defaultConfigDirFlag, "config directory")
	verifyEmailTrackerServerFlag := verifyEmailCommand.String("trackerServer", "tracker.store.samos.io:6677", "tracker server address, eg: tracker.store.samos.io:6677")
	verifyCodeFlag := verifyEmailCommand.String("verifyCode", "", "verify code from verify email")

	resendVerifyCodeCommand := flag.NewFlagSet("resendVerifyCode", flag.ExitOnError)
	resendVerifyCodeConfigDirFlag := resendVerifyCodeCommand.String("configDir", defaultConfigDirFlag, "config directory")
	resendVerifyCodeTrackerServerFlag := resendVerifyCodeCommand.String("trackerServer", "tracker.store.samos.io:6677", "tracker server address, eg: tracker.store.samos.io:6677")

	addStorageCommand := flag.NewFlagSet("addStorage", flag.ExitOnError)
	addStorageConfigDirFlag := addStorageCommand.String("configDir", defaultConfigDirFlag, "config directory")
	addStorageTrackerServerFlag := addStorageCommand.String("trackerServer", "tracker.store.samos.io:6677", "tracker server address, eg: tracker.store.samos.io:6677")
	pathFlag := addStorageCommand.String("path", "", "add storage path")
	volumeFlag := addStorageCommand.String("volume", "", "add storage volume size, unit TB or GB, eg: 2TB or 500GB")

	switchPrivateCommand := flag.NewFlagSet("switchPrivate", flag.ExitOnError)
	switchPrivateConfigDirFlag := switchPrivateCommand.String("configDir", defaultConfigDirFlag, "config directory")
	switchPrivateTrackerServerFlag := switchPrivateCommand.String("trackerServer", "tracker.store.samos.io:6677", "tracker server address, eg: tracker.store.samos.io:6677")

	switchPublicCommand := flag.NewFlagSet("switchPublic", flag.ExitOnError)
	switchPublicConfigDirFlag := switchPublicCommand.String("configDir", defaultConfigDirFlag, "config directory")
	switchPublicTrackerServerFlag := switchPublicCommand.String("trackerServer", "tracker.store.samos.io:6677", "tracker server address, eg: tracker.store.samos.io:6677")
	switchPublicListenFlag := switchPublicCommand.String("listen", ":6666", "listen address and port, eg: 111.111.111.111:6666 or :6666")
	switchPublicPortFlag := switchPublicCommand.Uint("port", 6666, "outer network port for client to connect, eg:6666")
	switchPublicHostFlag := switchPublicCommand.String("host", "", "outer ip or domain for client to connect, eg: 123.123.123.123")
	switchPublicDynamicDomainFlag := switchPublicCommand.String("dynamicDomain", "", "dynamic domain for client to connect, eg: mydomain.xicp.net")
	if len(os.Args) == 1 {
		fmt.Printf("usage: %s <command> [<args>]\n", os.Args[0])
		fmt.Println("The most commonly used commands are: ")
		fmt.Println(" register [-configDir config-dir] [-trackerServer tracker-server-and-port] [-collectorServer collector-server-and-port] [-listen listen-address-and-port] [-host outer-host] [-dynamicDomain dynamic-domain] [-port outer-port] -walletAddress wallet-address -billEmail bill-email -downBandwidth down-bandwidth -upBandwidth up-bandwidth -availability availability-percentage -mainStoragePath storage-path -mainStorageVolume storage-volume -extraStorage extra-storage-string")
		registerCommand.PrintDefaults()
		fmt.Println(" verifyEmail [-configDir config-dir] [-trackerServer tracker-server-and-port] -verifyCode verify-code")
		verifyEmailCommand.PrintDefaults()
		fmt.Println(" resendVerifyCode [-configDir config-dir] [-trackerServer tracker-server-and-port]")
		resendVerifyCodeCommand.PrintDefaults()
		fmt.Println(" daemon [-configDir config-dir] [-trackerServer tracker-server-and-port] [-listen listen-address-and-port] [-disableAutoRefreshIp] [-quiet]")
		daemonCommand.PrintDefaults()
		fmt.Println(" addStorage [-configDir config-dir] [-trackerServer tracker-server-and-port] -path storage-path -volume storage-volume")
		addStorageCommand.PrintDefaults()
		fmt.Println(" switchPrivate [-configDir config-dir] [-trackerServer tracker-server-and-port] ")
		switchPrivateCommand.PrintDefaults()
		fmt.Println(" switchPublic [-configDir config-dir] [-trackerServer tracker-server-and-port] [-listen listen-address-and-port] [-host outer-host] [-dynamicDomain dynamic-domain] [-port outer-port]")
		switchPublicCommand.PrintDefaults()
		os.Exit(101)
	}

	switch os.Args[1] {
	case "daemon":
		daemonCommand.Parse(os.Args[2:])
		daemon(*daemonConfigDirFlag, *daemonTrackerServerFlag, *daemonCollectorServerFlag, *daemonTaskServerFlag, *listenFlag, *disableAutoRefreshIpFlag, *quietFlag)
	case "register":
		registerCommand.Parse(os.Args[2:])
		register(*registerConfigDirFlag, *registerTrackerServerFlag, *registerListenFlag, *walletAddressFlag, *billEmailFlag, *availabilityFlag,
			*upBandwidthFlag, *downBandwidthFlag, *portFlag, *hostFlag, *dynamicDomainFlag, *mainStoragePathFlag, *mainStorageVolumeFlag, *extraStorageFlag)
	case "addStorage":
		addStorageCommand.Parse(os.Args[2:])
		addStorage(*addStorageConfigDirFlag, *addStorageTrackerServerFlag, *pathFlag, *volumeFlag)
	case "verifyEmail":
		verifyEmailCommand.Parse(os.Args[2:])
		verifyEmail(*verifyEmailConfigDirFlag, *verifyEmailTrackerServerFlag, *verifyCodeFlag)
	case "resendVerifyCode":
		resendVerifyCodeCommand.Parse(os.Args[2:])
		resendVerifyCode(*resendVerifyCodeConfigDirFlag, *resendVerifyCodeTrackerServerFlag)
	case "switchPrivate":
		switchPrivateCommand.Parse(os.Args[2:])
		switchPrivate(*switchPrivateConfigDirFlag, *switchPrivateTrackerServerFlag)
	case "switchPublic":
		switchPublicCommand.Parse(os.Args[2:])
		switchPublic(*switchPublicConfigDirFlag, *switchPublicTrackerServerFlag, *switchPublicListenFlag, *switchPublicPortFlag, *switchPublicHostFlag, *switchPublicDynamicDomainFlag)
	default:
		fmt.Printf("%q is not valid command.\n", os.Args[1])
		os.Exit(102)
	}
}
func verifyEmail(configDir string, trackerServer string, verifyCode string) {
	err := config.LoadConfig(configDir)
	if err != nil {
		if err == config.NoConfErr {
			fmt.Printf("Config file is not ready, please run \"%s register\" to register first\n", os.Args[0])
			os.Exit(200)
		} else if err == config.ConfVerifyErr {
			fmt.Println("Config file wrong, can not verify email.")
			os.Exit(201)
		}
		fmt.Println("failed to load config, can not verify email: " + err.Error())
		os.Exit(202)
	}
	if verifyCode == "" {
		fmt.Printf("verifyCode is required.\n")
		os.Exit(7)
	}
	conn, err := grpc.Dial(trackerServer, grpc.WithInsecure())
	if err != nil {
		fmt.Printf("RPC Dial failed: %s\n", err.Error())
		os.Exit(8)
	}
	defer conn.Close()
	prsc := trp_pb.NewProviderRegisterServiceClient(conn)
	code, errMsg, err := client.VerifyBillEmail(prsc, verifyCode)
	if err != nil {
		fmt.Printf("verifyEmail failed: %s\n", err.Error())
		os.Exit(9)
	}
	if code != 0 {
		fmt.Println(errMsg)
		os.Exit(10)
	}
	fmt.Println("verifyEmail success, you can start daemon now.")
}

func resendVerifyCode(configDir string, trackerServer string) {
	err := config.LoadConfig(configDir)
	if err != nil {
		if err == config.NoConfErr {
			fmt.Printf("Config file is not ready, please run \"%s register\" to register first\n", os.Args[0])
			os.Exit(200)
		} else if err == config.ConfVerifyErr {
			fmt.Println("Config file wrong, can not resend verify code email.")
			os.Exit(201)
		}
		fmt.Println("failed to load config, can not resend verify code email: " + err.Error())
		os.Exit(202)
	}
	conn, err := grpc.Dial(trackerServer, grpc.WithInsecure())
	if err != nil {
		fmt.Printf("RPC Dial failed: %s\n", err.Error())
		os.Exit(8)
	}
	defer conn.Close()
	prsc := trp_pb.NewProviderRegisterServiceClient(conn)
	success, err := client.ResendVerifyCode(prsc)
	if err != nil {
		fmt.Printf("resendVerifyCode failed: %s\n", err.Error())
		os.Exit(9)
	}
	if !success {
		fmt.Println("resendVerifyCode failed, please retry")
		os.Exit(10)
	}
	fmt.Println("resendVerifyCode success, you can verify bill email.")
}

func daemon(configDir string, trackerServer string, collectorServer string, taskServer string, listen string, disableAutoRefreshIpFlag bool, quietFlag bool) {
	err := config.LoadConfig(configDir)
	if err != nil {
		if err == config.NoConfErr {
			fmt.Printf("Config file is not ready, please run \"%s register\" to register first\n", os.Args[0])
			os.Exit(200)
		} else if err == config.ConfVerifyErr {
			fmt.Println("Config file wrong, can not start daemon.")
			os.Exit(201)
		}
		fmt.Println("failed to load config, can not start daemon: " + err.Error())
		os.Exit(202)
	}
	config.StartAutoCheck()
	defer config.StopAutoCheck()
	collector.Start(collectorServer)
	defer collector.Stop()
	var port int
	private := config.GetProviderConfig().Private
	providerServer := impl.NewProviderService(taskServer, private)
	if !private {
		port, err = strconv.Atoi(strings.Split(listen, ":")[1])
		if err != nil {
			fmt.Println("parse listen port error: " + err.Error())
			os.Exit(2)
		}
		// connect to router
		igd, err := upnp.Discover()
		if err != nil {
			fmt.Println("use upnp get router failed: " + err.Error())
		} else {
			err = igd.Forward(uint16(port), "Samos storage")
			if err != nil {
				fmt.Println("use upnp port mapping failed: " + err.Error())
			}
		}
		grpcServer := grpc.NewServer(grpc.MaxRecvMsgSize(520 * 1024))
		go startServer(listen, grpcServer, providerServer)
		defer grpcServer.GracefulStop()
	}
	cronRunner := cron.New()
	if private {
		fmt.Println("Starting samos private network node.")
		cronRunner.AddFunc("@every 5m", func() { client.PrivateAlive(trackerServer) })
	}
	if !disableAutoRefreshIpFlag && !config.GetProviderConfig().Ddns && !private {
		refreshIp(trackerServer, port, true)
		cronRunner.AddFunc("37 */2 * * * *", func() {
			refreshIp(trackerServer, port, false)
		})
	}
	fmt.Println("Node is running.")
	if !quietFlag {
		cronRunner.AddFunc("0 * * * * *", func() { fmt.Print(".") })
	}
	cronRunner.AddFunc("@every 1m", func() { providerServer.GetTask() })
	cronRunner.Start()
	defer cronRunner.Stop()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan
	providerServer.CloseTaskProcessor()
}

func startServer(listen string, grpcServer *grpc.Server, providerServer *impl.ProviderService) {
	lis, err := net.Listen("tcp", listen)
	if err != nil {
		fmt.Printf("failed to listen: %s, error: %s\n", listen, err.Error())
		os.Exit(3)
	}
	defer providerServer.Close()
	pb.RegisterProviderServiceServer(grpcServer, providerServer)
	grpcServer.Serve(lis)
}

func register(configDir string, trackerServer string, listen string, walletAddress string, billEmail string,
	availability string, upBandwidth uint, downBandwidth uint, port uint, host string, dynamicDomain string,
	mainStoragePath string, mainStorageVolume string, extraStorageFlag string) {
	if config.ConfigExists(configDir) {
		fmt.Println("config file is adready exsits: " + configDir)
		os.Exit(2)
	}
	if len(walletAddress) == 0 {
		fmt.Println("walletAddress is required.")
		os.Exit(3)
	}
	if _, err := cipher.DecodeBase58Address(walletAddress); err != nil {
		fmt.Printf("walletAddress is not valid:%s\n", walletAddress)
		os.Exit(4)
	}
	if len(billEmail) == 0 {
		fmt.Println("billEmail is required.")
		os.Exit(5)
	}
	email_re := regexp.MustCompile("^[a-zA-Z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$")
	if !email_re.MatchString(billEmail) {
		fmt.Printf("billEmail is not valid:%s\n", billEmail)
		os.Exit(6)
	}
	if len(availability) == 0 {
		fmt.Println("availability is required.")
		os.Exit(7)
	}
	if availability[len(availability)-1] != '%' {
		fmt.Printf("availability should be a percentage: %s\n", availability)
		os.Exit(8)
	}
	availFloat, err := strconv.ParseFloat(availability[:len(availability)-1], 64)
	if err != nil {
		fmt.Printf("availability: %s is not valid:%s\n", availability, err)
		os.Exit(9)
	}
	availFloat = availFloat / 100
	if availFloat < 0.97 {
		fmt.Println("availability must equal or more than 97%.")
		os.Exit(10)
	}
	if upBandwidth == 0 {
		fmt.Println("upBandwidth is required.")
		os.Exit(11)
	}
	upBandwidthBps := uint64(upBandwidth) * 1000 * 1000
	if downBandwidth == 0 {
		fmt.Println("downBandwidth is required.")
		os.Exit(12)
	}
	downBandwidthBps := uint64(downBandwidth) * 1000 * 1000
	if len(mainStorageVolume) == 0 {
		fmt.Println("mainStorageVolume is required.")
		os.Exit(13)
	}
	if port < 1 || port > 65535 {
		fmt.Println("port must between 1 and 65535.")
		os.Exit(14)
	}
	mainStorageVolumeByte, err := parseStorageVolume(mainStorageVolume)
	if err != nil {
		fmt.Println("mainStorageVolume parse error: " + err.Error())
		os.Exit(15)
	}
	total, free, err := disk.Space(mainStoragePath)
	if err != nil {
		fmt.Printf("read free space of path [%s] failed: %s\n", mainStoragePath, err.Error())
		os.Exit(16)
	}
	if total < mainStorageVolumeByte {
		fmt.Printf("path [%s] total space [%d] is less than %s\n", mainStoragePath, total, mainStorageVolume)
		os.Exit(17)
	}
	if free < mainStorageVolumeByte {
		fmt.Printf("path [%s] free space [%d] is less than %s\n", mainStoragePath, free, mainStorageVolume)
		os.Exit(18)
	}
	extraStorage := make([]config.ExtraStorageInfo, 0, 8)
	if len(extraStorageFlag) > 0 {
		arr := strings.Split(extraStorageFlag, ",")
		if len(arr) == 0 {
			fmt.Printf("extraStorage format error: %s\n", extraStorageFlag)
			os.Exit(19)
		}
		if len(arr) > 255 {
			fmt.Println("do not support more than 255 extra storage")
			os.Exit(20)
		}
		var index byte = 1
		for _, str := range arr {
			unit := strings.Split(str, ":")
			if len(unit) != 2 {
				fmt.Printf("extraStorage format error: %s, wrong unit: %s\n", extraStorageFlag, str)
				os.Exit(21)
			}
			volume, err := parseStorageVolume(unit[1])
			if err != nil {
				fmt.Printf("extraStorage path %s parse error: %s\n", unit[0], err.Error())
				os.Exit(22)
			}
			total, free, err = disk.Space(unit[0])
			if err != nil {
				fmt.Printf("read free space of extraStorage path [%s] failed: %s\n", unit[0], err.Error())
				os.Exit(23)
			}
			if total < volume {
				fmt.Printf("extraStorage path [%s] total space [%d] is less than %d\n", unit[0], total, volume)
				os.Exit(24)
			}
			if free < volume {
				fmt.Printf("extraStorage path [%s] free space [%d] is less than %d\n", unit[0], free, volume)
				os.Exit(25)
			}
			if strings.Index(unit[0], mainStoragePath) == 0 {
				fmt.Printf("can not use %s as storage path, %s is already as storage path\n", unit[0], mainStoragePath)
				os.Exit(26)
			}
			for _, esi := range extraStorage {
				if strings.Index(unit[0], esi.Path) == 0 {
					fmt.Printf("can not use %s as storage path, %s is already as storage path\n", unit[0], esi.Path)
					os.Exit(27)
				}
			}
			extraStorage = append(extraStorage, config.ExtraStorageInfo{Path: unit[0], Volume: volume, Index: index})
			index++
		}
	}
	// TODO speed test
	testUpBandwidthBps := upBandwidthBps
	testDownBandwidthBps := downBandwidthBps
	doRegister(configDir, trackerServer, listen, walletAddress, billEmail, availFloat, upBandwidthBps, downBandwidthBps, testUpBandwidthBps, testDownBandwidthBps, uint32(port), host, dynamicDomain, mainStoragePath, mainStorageVolumeByte, extraStorage)
}

func parseStorageVolume(volStr string) (volume uint64, err error) {
	volStr = strings.ToUpper(volStr)
	if volStr[len(volStr)-1] == 'B' {
		volStr = volStr[:len(volStr)-1]
	}
	if volStr[len(volStr)-1] == 'G' {
		val, err := strconv.ParseInt(volStr[:len(volStr)-1], 10, 64)
		if err != nil {
			return 0, err
		}
		if os.Getenv("NEBULA_TEST_MODE") == "1" {
			if val < 10 {
				return 0, errors.New("storage volume must equal or more than 10G")
			}
		} else {
			if val < 100 {
				return 0, errors.New("storage volume must equal or more than 100G")
			}
		}
		return uint64(val) * 1000 * 1000 * 1000, nil
	} else if volStr[len(volStr)-1] == 'T' {
		val, err := strconv.ParseInt(volStr[:len(volStr)-1], 10, 64)
		if err != nil {
			return 0, err
		}
		return uint64(val) * 1000 * 1000 * 1000 * 1000, nil
	} else {
		return 0, errors.New("not valid, must end with G,T or GB,TB")
	}
}

func encrypt(pubKey *rsa.PublicKey, data []byte) []byte {
	res, err := util_rsa.EncryptLong(pubKey, data, node.RSA_KEY_BYTES)
	if err != nil {
		fmt.Println("public key encrypt error: " + err.Error())
		os.Exit(300)
	}
	return res
}

func doRegister(configDir string, trackerServer string, listen string, walletAddress string, billEmail string,
	availability float64, upBandwidth uint64, downBandwidth uint64,
	testUpBandwidth uint64, testDownBandwidth uint64, port uint32, host string,
	dynamicDomain string, mainStoragePath string, mainStorageVolume uint64, extraStorage []config.ExtraStorageInfo) {
	no := node.NewNode(10)
	pc := newProviderConfig(no, walletAddress, billEmail, availability, upBandwidth, downBandwidth, mainStoragePath, mainStorageVolume, extraStorage)
	extraStorageSlice := make([]uint64, 0, len(extraStorage))
	for _, v := range extraStorage {
		extraStorageSlice = append(extraStorageSlice, v.Volume)
	}
	// connect to router
	igd, err := upnp.Discover()
	if err != nil {
		fmt.Println("use upnp get router failed: " + err.Error())
	} else {
		err = igd.Forward(uint16(port), "Samos storage")
		if err != nil {
			fmt.Println("use upnp port mapping failed: " + err.Error())
		}
		// discover external IP
		externalIp, err := igd.ExternalIP()
		if err != nil {
			fmt.Println("use upnp get outer ip failed: " + err.Error())
		} else {
			fmt.Println("use upnp get outer ip is: " + externalIp)
		}
	}
	grpcServer := grpc.NewServer(grpc.MaxRecvMsgSize(520 * 1024))
	go startPingServer(listen, grpcServer, util_hash.Sha1(no.NodeId))
	defer grpcServer.GracefulStop()
	time.Sleep(time.Duration(5) * time.Second) //for loadbalance health check
	conn, err := grpc.Dial(trackerServer, grpc.WithInsecure())
	if err != nil {
		fmt.Printf("RPC Dial failed: %s\n", err.Error())
		os.Exit(52)
	}
	defer conn.Close()
	prsc := trp_pb.NewProviderRegisterServiceClient(conn)
	pubKeyBytes, publicKeyHash, clientIp, err := client.GetPublicKey(prsc)
	if err != nil {
		fmt.Printf("GetPublicKey failed: %s\n", err.Error())
		os.Exit(53)
	}
	if host == "" && dynamicDomain == "" {
		fmt.Println("not specify host and dynamic domain, will use: " + clientIp)
		host = clientIp
	}
	pubKey, err := x509.ParsePKCS1PublicKey(pubKeyBytes)
	if err != nil {
		fmt.Printf("Parse PublicKey failed: %s\n", err.Error())
		os.Exit(54)
	}
	success := false
	privateNetwork := false
	for times := 0; times < 5; times++ {
		code, errMsg, err := client.RegisterPublic(prsc, publicKeyHash, encrypt(pubKey, no.NodeId),
			encrypt(pubKey, no.PubKeyBytes), encrypt(pubKey, no.EncryptKey["0"]), encrypt(pubKey, []byte(pc.WalletAddress)),
			encrypt(pubKey, []byte(pc.BillEmail)), mainStorageVolume, upBandwidth, downBandwidth,
			testUpBandwidth, testDownBandwidth, availability, port, encrypt(pubKey, []byte(host)), encrypt(pubKey, []byte(dynamicDomain)), extraStorageSlice, no.PriKey)
		if err != nil {
			fmt.Println("Register failed: " + err.Error())
			os.Exit(55)
		}
		if code == 0 {
			if len(host) == 0 && len(dynamicDomain) > 0 {
				pc.Ddns = true
			}
			success = true
			break
		} else {
			if code == 300 {
				fmt.Println(errMsg)
				fmt.Println("Retrying...")
				continue
			}
			if code == 500 {
				pubKeyBytes, publicKeyHash, _, err = client.GetPublicKey(prsc)
				if err != nil {
					fmt.Printf("GetPublicKey failed: %s\n", err.Error())
					os.Exit(53)
				}
				pubKey, err = x509.ParsePKCS1PublicKey(pubKeyBytes)
				if err != nil {
					fmt.Printf("Parse PublicKey failed: %s\n", err.Error())
					os.Exit(54)
				}
				continue
			}
			if code == 27 {
				h := host
				if len(host) == 0 {
					h = dynamicDomain
				}
				err := keyboard.Open()
				if err != nil {
					fmt.Printf("Prepare read from keyboard error: %s\n", err.Error())
					os.Exit(56)
				}
				defer keyboard.Close()
				fmt.Printf("Ping %s:%d failed, You may not have a public network ip, error message: %s\n", h, port, errMsg)
				fmt.Printf("If you want to register as a private network node, press Y and press another key to terminate the registration process. [Y/N]")
				char, _, err := keyboard.GetKey()
				if err != nil {
					fmt.Printf("Read from keyboard error: %s\n", err.Error())
					os.Exit(57)
				}
				if char == 'y' || char == 'Y' {
					privateNetwork = true
					break
				} else {
					fmt.Println("Register failed")
					os.Exit(58)
				}
			} else {
				fmt.Printf("Error Code: %d, error message:%s\n", code, errMsg)
				os.Exit(59)
			}
		}
	}
	if privateNetwork {
		for times := 0; times < 5; times++ {
			code, errMsg, err := client.RegisterPrivate(prsc, publicKeyHash, encrypt(pubKey, no.NodeId),
				encrypt(pubKey, no.PubKeyBytes), encrypt(pubKey, no.EncryptKey["0"]), encrypt(pubKey, []byte(pc.WalletAddress)),
				encrypt(pubKey, []byte(pc.BillEmail)), mainStorageVolume, upBandwidth, downBandwidth,
				testUpBandwidth, testDownBandwidth, availability, extraStorageSlice, no.PriKey)
			if err != nil {
				fmt.Println("Register failed: " + err.Error())
				os.Exit(55)
			}
			if code == 0 {
				pc.Private = true
				success = true
				break
			} else {
				if code == 300 {
					fmt.Println(errMsg)
					fmt.Println("Retrying...")
					continue
				}
				if code == 500 {
					pubKeyBytes, publicKeyHash, _, err = client.GetPublicKey(prsc)
					if err != nil {
						fmt.Printf("GetPublicKey failed: %s\n", err.Error())
						os.Exit(53)
					}
					pubKey, err = x509.ParsePKCS1PublicKey(pubKeyBytes)
					if err != nil {
						fmt.Printf("Parse PublicKey failed: %s\n", err.Error())
						os.Exit(54)
					}
					continue
				}
				fmt.Printf("Error Code: %d, error message:%s\n", code, errMsg)
				os.Exit(59)
			}
		}
	}
	if success {
		path := config.CreateProviderConfig(configDir, pc)
		fmt.Println("Register success, please recieve verify code email to verify bill email and backup your config file: " + path)
		if privateNetwork {
			fmt.Println("Notice: registered as a private network node")
		}
	} else {
		fmt.Println("Registration failed, the maximum number of retries was reached")
	}
}

func startPingServer(listen string, grpcServer *grpc.Server, nodeIdHash []byte) {
	lis, err := net.Listen("tcp", listen)
	if err != nil {
		fmt.Printf("failed to listen: %s, error: %s\n", listen, err.Error())
		os.Exit(57)
	}
	pb.RegisterProviderServiceServer(grpcServer, &pingProviderService{nodeIdHash: nodeIdHash})
	grpcServer.Serve(lis)
}

type pingProviderService struct {
	nodeIdHash []byte
}

func (self *pingProviderService) Ping(ctx context.Context, req *pb.PingReq) (*pb.PingResp, error) {
	return &pb.PingResp{NodeIdHash: self.nodeIdHash}, nil
}
func (self *pingProviderService) Store(stream pb.ProviderService_StoreServer) error {
	return nil
}
func (self *pingProviderService) StoreSmall(ctx context.Context, req *pb.StoreReq) (*pb.StoreResp, error) {
	return nil, nil
}
func (self *pingProviderService) Retrieve(req *pb.RetrieveReq, stream pb.ProviderService_RetrieveServer) error {
	return nil
}
func (self *pingProviderService) RetrieveSmall(ctx context.Context, req *pb.RetrieveReq) (*pb.RetrieveResp, error) {
	return nil, nil
}
func (self *pingProviderService) Remove(ctx context.Context, req *pb.RemoveReq) (*pb.RemoveResp, error) {
	return nil, nil
}
func (self *pingProviderService) GetFragment(ctx context.Context, req *pb.GetFragmentReq) (*pb.GetFragmentResp, error) {
	return nil, nil
}
func (self *pingProviderService) CheckAvailable(ctx context.Context, req *pb.CheckAvailableReq) (resp *pb.CheckAvailableResp, err error) {
	return nil, nil
}
func addStorage(configDir string, trackerServer string, path string, volumeStr string) {
	volume, err := parseStorageVolume(volumeStr)
	if err != nil {
		fmt.Printf("storage path %s parse error: %s\n", path, err.Error())
		os.Exit(2)
	}
	total, free, err := disk.Space(path)
	if err != nil {
		fmt.Printf("read free space of storage path [%s] failed: %s\n", path, err.Error())
		os.Exit(3)
	}
	if total < volume {
		fmt.Printf("storage path [%s] total space [%d] is less than %d\n", path, total, volume)
		os.Exit(4)
	}
	if free < volume {
		fmt.Printf("storage path [%s] free space [%d] is less than %d\n", path, free, volume)
		os.Exit(5)
	}
	err = config.LoadConfig(configDir)
	if err != nil {
		if err == config.NoConfErr {
			fmt.Printf("Config file is not ready, please run \"%s register\" to register first\n", os.Args[0])
			os.Exit(200)
		} else if err == config.ConfVerifyErr {
			fmt.Println("Config file wrong, can not add storage.")
			os.Exit(201)
		}
		fmt.Println("failed to load config, can not add storage: " + err.Error())
		os.Exit(202)
	}
	pc := config.GetProviderConfig()
	if len(pc.ExtraStorage) == 0 {
		pc.ExtraStorage = make([]config.ExtraStorageInfo, 0, 1)
	} else if len(pc.ExtraStorage) == 255 {
		fmt.Println("do not support more than 255 extra storage")
		os.Exit(6)
	}
	if strings.Index(path, pc.MainStoragePath) == 0 {
		fmt.Printf("can not use %s as storage path, %s is already as storage path\n", path, pc.MainStoragePath)
		os.Exit(7)
	}
	for _, v := range pc.ExtraStorage {
		if strings.Index(path, v.Path) == 0 {
			fmt.Printf("can not use %s as storage path, %s is already as storage path\n", path, v.Path)
			os.Exit(8)
		}
	}
	conn, err := grpc.Dial(trackerServer, grpc.WithInsecure())
	if err != nil {
		fmt.Printf("RPC Dial failed: %s\n", err.Error())
		os.Exit(9)
	}
	defer conn.Close()
	prsc := trp_pb.NewProviderRegisterServiceClient(conn)
	success, err := client.AddExtraStorage(prsc, volume)
	if err != nil {
		fmt.Printf("resendVerifyCode failed: %s\n", err.Error())
		os.Exit(10)
	}
	if !success {
		fmt.Println("resendVerifyCode failed, please retry")
		os.Exit(11)
	}
	idx := byte(len(pc.ExtraStorage) + 1)
	pc.ExtraStorage = append(pc.ExtraStorage, config.ExtraStorageInfo{Path: path,
		Volume: volume,
		Index:  idx})
	config.SaveProviderConfig()
	fmt.Println("Add storage success, please backup your config file: " + config.GetConfigFullPath(configDir))
}

func switchPrivate(configDir string, trackerServer string) {
	err := config.LoadConfig(configDir)
	if err != nil {
		if err == config.NoConfErr {
			fmt.Printf("Config file is not ready, please run \"%s register\" to register first\n", os.Args[0])
			os.Exit(200)
		} else if err == config.ConfVerifyErr {
			fmt.Println("Config file wrong, can not switchPrivate.")
			os.Exit(201)
		}
		fmt.Println("failed to load config, can not switchPrivate: " + err.Error())
		os.Exit(202)
	}
	pc := config.GetProviderConfig()
	err = keyboard.Open()
	if err != nil {
		fmt.Printf("Prepare read from keyboard error: %s\n", err.Error())
		os.Exit(1)
	}
	defer keyboard.Close()
	fmt.Printf("The rewards obtained after switching to the private network node will be greatly reduced. Press Y if you want to switch, press the other keys to cancel the switch. [Y/N]")
	char, _, err := keyboard.GetKey()
	if err != nil {
		fmt.Printf("Read from keyboard error: %s\n", err.Error())
		os.Exit(2)
	}
	if char == 'y' || char == 'Y' {
		conn, err := grpc.Dial(trackerServer, grpc.WithInsecure())
		if err != nil {
			fmt.Printf("RPC Dial failed: %s\n", err.Error())
			os.Exit(9)
		}
		defer conn.Close()
		prsc := trp_pb.NewProviderRegisterServiceClient(conn)
		success, err := client.SwitchPrivate(prsc)
		if err != nil {
			fmt.Printf("resendVerifyCode failed: %s\n", err.Error())
			os.Exit(10)
		}
		if !success {
			fmt.Println("resendVerifyCode failed, please retry")
			os.Exit(11)
		}
		pc.Private = true
		config.SaveProviderConfig()
		fmt.Println("Switch to private network node success, please backup your config file: " + config.GetConfigFullPath(configDir))
	} else {
		fmt.Println("Switch has been canceled")
	}
}

func switchPublic(configDir string, trackerServer string, listen string, port uint, host string,
	dynamicDomain string) {
	if port < 1 || port > 65535 {
		fmt.Println("port must between 1 and 65535.")
		os.Exit(1)
	}
	err := config.LoadConfig(configDir)
	if err != nil {
		if err == config.NoConfErr {
			fmt.Printf("Config file is not ready, please run \"%s register\" to register first\n", os.Args[0])
			os.Exit(200)
		} else if err == config.ConfVerifyErr {
			fmt.Println("Config file wrong, can not switchPublic.")
			os.Exit(201)
		}
		fmt.Println("failed to load config, can not switchPublic: " + err.Error())
		os.Exit(202)
	}
	pc := config.GetProviderConfig()
	// connect to router
	igd, err := upnp.Discover()
	if err != nil {
		fmt.Println("use upnp get router failed: " + err.Error())
	} else {
		err = igd.Forward(uint16(port), "Samos storage")
		if err != nil {
			fmt.Println("use upnp port mapping failed: " + err.Error())
		}
		// discover external IP
		externalIp, err := igd.ExternalIP()
		if err != nil {
			fmt.Println("use upnp get outer ip failed: " + err.Error())
		} else {
			fmt.Println("use upnp get outer ip is: " + externalIp)
		}
	}
	nodeId, _, _, _, _, err := config.ParseNode()
	if err != nil {
		fmt.Println("Parse nodeId error: " + err.Error())
		os.Exit(2)
	}
	grpcServer := grpc.NewServer(grpc.MaxRecvMsgSize(520 * 1024))
	go startPingServer(listen, grpcServer, util_hash.Sha1(nodeId))
	defer grpcServer.GracefulStop()
	time.Sleep(time.Duration(5) * time.Second) //for loadbalance health check
	conn, err := grpc.Dial(trackerServer, grpc.WithInsecure())
	if err != nil {
		fmt.Printf("RPC Dial failed: %s\n", err.Error())
		os.Exit(3)
	}
	defer conn.Close()
	prsc := trp_pb.NewProviderRegisterServiceClient(conn)
	pubKeyBytes, publicKeyHash, clientIp, err := client.GetPublicKey(prsc)
	if err != nil {
		fmt.Printf("GetPublicKey failed: %s\n", err.Error())
		os.Exit(4)
	}
	if host == "" && dynamicDomain == "" {
		fmt.Println("not specify host and dynamic domain, will use: " + clientIp)
		host = clientIp
	}
	pubKey, err := x509.ParsePKCS1PublicKey(pubKeyBytes)
	if err != nil {
		fmt.Printf("Parse PublicKey failed: %s\n", err.Error())
		os.Exit(5)
	}
	for times := 0; times < 5; times++ {
		code, errMsg, err := client.SwitchPublic(prsc, publicKeyHash, uint32(port), encrypt(pubKey, []byte(host)), encrypt(pubKey, []byte(dynamicDomain)))
		if err != nil {
			fmt.Println("SwitchPublic failed: " + err.Error())
			os.Exit(6)
		}
		if code == 0 {
			pc.Ddns = (len(host) == 0 && len(dynamicDomain) > 0)
			pc.Private = false
			config.SaveProviderConfig()
			fmt.Println("Switch to public network node success, please backup your config file: " + config.GetConfigFullPath(configDir))
			return
		} else {
			if code == 300 {
				fmt.Println(errMsg)
				fmt.Println("Retrying...")
				continue
			}
			if code == 500 {
				pubKeyBytes, publicKeyHash, _, err = client.GetPublicKey(prsc)
				if err != nil {
					fmt.Printf("GetPublicKey failed: %s\n", err.Error())
					os.Exit(7)
				}
				pubKey, err = x509.ParsePKCS1PublicKey(pubKeyBytes)
				if err != nil {
					fmt.Printf("Parse PublicKey failed: %s\n", err.Error())
					os.Exit(8)
				}
				continue
			}
			if code == 27 {
				h := host
				if len(host) == 0 {
					h = dynamicDomain
				}
				fmt.Printf("SwitchPublic failed, ping %s:%d failed, You may not have a public network ip, error message: %s\n", h, port, errMsg)
				os.Exit(9)
			} else {
				fmt.Printf("Error Code: %d, error message:%s\n", code, errMsg)
				os.Exit(10)
			}
		}
	}
	fmt.Println("SwitchPublic failed, the maximum number of retries was reached")
}

func newProviderConfig(no *node.Node, walletAddress string, billEmail string,
	availability float64, upBandwidth uint64, downBandwidth uint64,
	mainStoragePath string, mainStorageVolume uint64, extraStorage []config.ExtraStorageInfo) *config.ProviderConfig {
	pc := &config.ProviderConfig{
		NodeId:            no.NodeIdStr(),
		WalletAddress:     walletAddress,
		BillEmail:         billEmail,
		PublicKey:         no.PublicKeyStr(),
		PrivateKey:        no.PrivateKeyStr(),
		Availability:      availability,
		MainStoragePath:   mainStoragePath,
		MainStorageVolume: mainStorageVolume,
		UpBandwidth:       upBandwidth,
		DownBandwidth:     downBandwidth,
		ExtraStorage:      extraStorage,
	}
	m := make(map[string]string, len(no.EncryptKey))
	for k, v := range no.EncryptKey {
		m[k] = hex.EncodeToString(v)
	}
	pc.EncryptKey = m
	return pc
}

func refreshIp(trackerServer string, providerPort int, exitOnError bool) (ip string) {
	conn, err := grpc.Dial(trackerServer, grpc.WithInsecure())
	if err != nil {
		if exitOnError {
			fmt.Printf("RPC Dial failed: %s\n", err.Error())
			os.Exit(61)
		} else {
			log.Warningf("RPC Dial failed when refresh ip, info: %s", err)
			return
		}
	}
	defer conn.Close()
	prsc := trp_pb.NewProviderRegisterServiceClient(conn)
	ip, err = client.RefreshIp(prsc, uint32(providerPort))
	if err != nil {
		if exitOnError {
			fmt.Printf("refresh ip failed: %s\n", err.Error())
			os.Exit(62)
		} else {
			log.Warningf("refresh ip failed, info: %s", err)
			return
		}
	}
	return
}
