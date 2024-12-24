package service

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/1Panel-dev/1Panel/core/app/dto"
	"github.com/1Panel-dev/1Panel/core/app/repo"
	"github.com/1Panel-dev/1Panel/core/buserr"
	"github.com/1Panel-dev/1Panel/core/constant"
	"github.com/1Panel-dev/1Panel/core/global"
	"github.com/1Panel-dev/1Panel/core/utils/cmd"
	"github.com/1Panel-dev/1Panel/core/utils/common"
	"github.com/1Panel-dev/1Panel/core/utils/encrypt"
	"github.com/1Panel-dev/1Panel/core/utils/firewall"
	"github.com/1Panel-dev/1Panel/core/utils/xpack"
	"github.com/gin-gonic/gin"
)

type SettingService struct{}

type ISettingService interface {
	GetSettingInfo() (*dto.SettingInfo, error)
	LoadInterfaceAddr() ([]string, error)
	Update(key, value string) error
	UpdateProxy(req dto.ProxyUpdate) error
	UpdatePassword(c *gin.Context, old, new string) error
	UpdatePort(port uint) error
	UpdateBindInfo(req dto.BindInfo) error
	UpdateSSL(c *gin.Context, req dto.SSLUpdate) error
	LoadFromCert() (*dto.SSLInfo, error)
	HandlePasswordExpired(c *gin.Context, old, new string) error

	GetTerminalInfo() (*dto.TerminalInfo, error)
	UpdateTerminal(req dto.TerminalInfo) error

	UpdateSystemSSL() error
}

func NewISettingService() ISettingService {
	return &SettingService{}
}

func (u *SettingService) GetSettingInfo() (*dto.SettingInfo, error) {
	setting, err := settingRepo.List()
	if err != nil {
		return nil, constant.ErrRecordNotFound
	}
	settingMap := make(map[string]string)
	for _, set := range setting {
		settingMap[set.Key] = set.Value
	}
	var info dto.SettingInfo
	arr, err := json.Marshal(settingMap)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(arr, &info); err != nil {
		return nil, err
	}
	if info.ProxyPasswdKeep != constant.StatusEnable {
		info.ProxyPasswd = ""
	} else {
		info.ProxyPasswd, _ = encrypt.StringDecrypt(info.ProxyPasswd)
	}

	return &info, err
}

func (u *SettingService) Update(key, value string) error {
	oldVal, err := settingRepo.Get(repo.WithByKey(key))
	if err != nil {
		return err
	}
	if oldVal.Value == value {
		return nil
	}
	switch key {
	case "AppStoreLastModified":
		exist, _ := settingRepo.Get(repo.WithByKey("AppStoreLastModified"))
		if exist.ID == 0 {
			_ = settingRepo.Create("AppStoreLastModified", value)
			return nil
		}
	}

	if err := settingRepo.Update(key, value); err != nil {
		return err
	}

	switch key {
	case "ExpirationDays":
		timeout, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		if err := settingRepo.Update("ExpirationTime", time.Now().AddDate(0, 0, timeout).Format(constant.DateTimeLayout)); err != nil {
			return err
		}
	case "BindDomain":
		if len(value) != 0 {
			_ = global.SESSION.Clean()
		}
	case "UserName", "Password":
		_ = global.SESSION.Clean()
	case "MasterAddr":
		go func() {
			if err := xpack.UpdateMasterAddr(value); err != nil {
				global.LOG.Errorf("update master addr failed, err: %v", err)
			}
		}()
	}

	return nil
}

func (u *SettingService) LoadInterfaceAddr() ([]string, error) {
	addrMap := make(map[string]struct{})
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if ok && ipNet.IP.To16() != nil {
			addrMap[ipNet.IP.String()] = struct{}{}
		}
	}
	var data []string
	for key := range addrMap {
		data = append(data, key)
	}
	return data, nil
}

func (u *SettingService) UpdateBindInfo(req dto.BindInfo) error {
	if err := settingRepo.Update("Ipv6", req.Ipv6); err != nil {
		return err
	}
	if err := settingRepo.Update("BindAddress", req.BindAddress); err != nil {
		return err
	}
	go func() {
		time.Sleep(1 * time.Second)
		_, err := cmd.Exec("systemctl restart 1panel.service")
		if err != nil {
			global.LOG.Errorf("restart system with new bind info failed, err: %v", err)
		}
	}()
	return nil
}

func (u *SettingService) UpdateProxy(req dto.ProxyUpdate) error {
	if err := settingRepo.Update("ProxyUrl", req.ProxyUrl); err != nil {
		return err
	}
	if err := settingRepo.Update("ProxyType", req.ProxyType); err != nil {
		return err
	}
	if err := settingRepo.Update("ProxyPort", req.ProxyPort); err != nil {
		return err
	}
	if err := settingRepo.Update("ProxyUser", req.ProxyUser); err != nil {
		return err
	}
	pass, _ := encrypt.StringEncrypt(req.ProxyPasswd)
	if err := settingRepo.Update("ProxyPasswd", pass); err != nil {
		return err
	}
	if err := settingRepo.Update("ProxyPasswdKeep", req.ProxyPasswdKeep); err != nil {
		return err
	}
	return nil
}

func (u *SettingService) UpdatePort(port uint) error {
	if common.ScanPort(int(port)) {
		return buserr.WithDetail(constant.ErrPortInUsed, port, nil)
	}
	oldPort, err := settingRepo.Get(repo.WithByKey("ServerPort"))
	if err != nil {
		return err
	}
	if oldPort.Value == fmt.Sprintf("%v", port) {
		return nil
	}
	if err := firewall.UpdatePort(oldPort.Value, fmt.Sprintf("%v", port)); err != nil {
		return err
	}

	if err := settingRepo.Update("ServerPort", strconv.Itoa(int(port))); err != nil {
		return err
	}
	go func() {
		time.Sleep(1 * time.Second)
		defer func() {
			if _, err := cmd.Exec("systemctl restart 1panel.service"); err != nil {
				global.LOG.Errorf("restart system port failed, err: %v", err)
			}
		}()

		masterAddr, err := settingRepo.Get(repo.WithByKey("MasterAddr"))
		if err != nil {
			global.LOG.Errorf("load master addr from db failed, err: %v", err)
			return
		}
		if len(masterAddr.Value) != 0 {
			oldMasterPort := loadPort(masterAddr.Value)
			if len(oldMasterPort) != 0 {
				newMasterAddr := strings.ReplaceAll(masterAddr.Value, oldMasterPort, fmt.Sprintf("%v", port))
				_ = settingRepo.Update("MasterAddr", newMasterAddr)
				if err := xpack.UpdateMasterAddr(newMasterAddr); err != nil {
					global.LOG.Errorf("update master addr from db failed, err: %v", err)
					return
				}
			}
		}
	}()
	return nil
}

func (u *SettingService) UpdateSSL(c *gin.Context, req dto.SSLUpdate) error {
	secretDir := path.Join(global.CONF.System.BaseDir, "1panel/secret")
	if req.SSL == constant.StatusDisable {
		if err := settingRepo.Update("SSL", constant.StatusDisable); err != nil {
			return err
		}
		if err := settingRepo.Update("SSLType", "self"); err != nil {
			return err
		}
		_ = os.Remove(path.Join(secretDir, "server.crt"))
		_ = os.Remove(path.Join(secretDir, "server.key"))
		return nil
	}
	if _, err := os.Stat(secretDir); err != nil && os.IsNotExist(err) {
		if err = os.MkdirAll(secretDir, os.ModePerm); err != nil {
			return err
		}
	}
	if err := settingRepo.Update("SSLType", req.SSLType); err != nil {
		return err
	}
	var (
		secret string
		key    string
	)

	switch req.SSLType {
	case "import-paste":
		secret = req.Cert
		key = req.Key
	case "import-local":
		keyFile, err := os.ReadFile(req.Key)
		if err != nil {
			return err
		}
		key = string(keyFile)
		certFile, err := os.ReadFile(req.Cert)
		if err != nil {
			return err
		}
		secret = string(certFile)
	}

	if err := os.WriteFile(path.Join(secretDir, "server.crt.tmp"), []byte(secret), 0600); err != nil {
		return err
	}
	if err := os.WriteFile(path.Join(secretDir, "server.key.tmp"), []byte(key), 0600); err != nil {
		return err
	}
	if err := checkCertValid(); err != nil {
		return err
	}
	if err := os.Rename(path.Join(secretDir, "server.crt.tmp"), path.Join(secretDir, "server.crt")); err != nil {
		return err
	}
	if err := os.Rename(path.Join(secretDir, "server.key.tmp"), path.Join(secretDir, "server.key")); err != nil {
		return err
	}
	if err := settingRepo.Update("SSL", req.SSL); err != nil {
		return err
	}

	if err := u.UpdateSystemSSL(); err != nil {
		return err
	}

	go func() {
		oldSSL, _ := settingRepo.Get(repo.WithByKey("SSL"))
		if oldSSL.Value != req.SSL {
			masterAddr, err := settingRepo.Get(repo.WithByKey("MasterAddr"))
			if err != nil {
				global.LOG.Errorf("load master addr from db failed, err: %v", err)
				return
			}
			addrItem := masterAddr.Value
			if req.SSL == constant.StatusDisable {
				addrItem = strings.ReplaceAll(addrItem, "https://", "http://")
			} else {
				addrItem = strings.ReplaceAll(addrItem, "http://", "https://")
			}
			_ = settingRepo.Update("MasterAddr", addrItem)
			if err := xpack.UpdateMasterAddr(addrItem); err != nil {
				global.LOG.Errorf("update master addr from db failed, err: %v", err)
			}
		}
	}()
	return nil
}

func (u *SettingService) LoadFromCert() (*dto.SSLInfo, error) {
	ssl, err := settingRepo.Get(repo.WithByKey("SSL"))
	if err != nil {
		return nil, err
	}
	if ssl.Value == constant.StatusDisable {
		return &dto.SSLInfo{}, nil
	}
	sslType, err := settingRepo.Get(repo.WithByKey("SSLType"))
	if err != nil {
		return nil, err
	}
	var data dto.SSLInfo
	switch sslType.Value {
	case "self":
		data, err = loadInfoFromCert()
		if err != nil {
			return nil, err
		}
	case "import-paste", "import-local":
		data, err = loadInfoFromCert()
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(path.Join(global.CONF.System.BaseDir, "1panel/secret/server.crt")); err != nil {
			return nil, fmt.Errorf("load server.crt file failed, err: %v", err)
		}
		certFile, _ := os.ReadFile(path.Join(global.CONF.System.BaseDir, "1panel/secret/server.crt"))
		data.Cert = string(certFile)

		if _, err := os.Stat(path.Join(global.CONF.System.BaseDir, "1panel/secret/server.key")); err != nil {
			return nil, fmt.Errorf("load server.key file failed, err: %v", err)
		}
		keyFile, _ := os.ReadFile(path.Join(global.CONF.System.BaseDir, "1panel/secret/server.key"))
		data.Key = string(keyFile)
	case "select":
		// TODO select ssl from website
	}
	return &data, nil
}

func (u *SettingService) HandlePasswordExpired(c *gin.Context, old, new string) error {
	setting, err := settingRepo.Get(repo.WithByKey("Password"))
	if err != nil {
		return err
	}
	passwordFromDB, err := encrypt.StringDecrypt(setting.Value)
	if err != nil {
		return err
	}
	if passwordFromDB == old {
		newPassword, err := encrypt.StringEncrypt(new)
		if err != nil {
			return err
		}
		if err := settingRepo.Update("Password", newPassword); err != nil {
			return err
		}

		expiredSetting, err := settingRepo.Get(repo.WithByKey("ExpirationDays"))
		if err != nil {
			return err
		}
		timeout, _ := strconv.Atoi(expiredSetting.Value)
		if err := settingRepo.Update("ExpirationTime", time.Now().AddDate(0, 0, timeout).Format(constant.DateTimeLayout)); err != nil {
			return err
		}
		return nil
	}
	return constant.ErrInitialPassword
}

func (u *SettingService) GetTerminalInfo() (*dto.TerminalInfo, error) {
	setting, err := settingRepo.List()
	if err != nil {
		return nil, constant.ErrRecordNotFound
	}
	settingMap := make(map[string]string)
	for _, set := range setting {
		settingMap[set.Key] = set.Value
	}
	var info dto.TerminalInfo
	arr, err := json.Marshal(settingMap)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(arr, &info); err != nil {
		return nil, err
	}
	return &info, err
}
func (u *SettingService) UpdateTerminal(req dto.TerminalInfo) error {
	if err := settingRepo.Update("LineHeight", req.LineHeight); err != nil {
		return err
	}
	if err := settingRepo.Update("LetterSpacing", req.LetterSpacing); err != nil {
		return err
	}
	if err := settingRepo.Update("FontSize", req.FontSize); err != nil {
		return err
	}
	if err := settingRepo.Update("CursorBlink", req.CursorBlink); err != nil {
		return err
	}
	if err := settingRepo.Update("CursorStyle", req.CursorStyle); err != nil {
		return err
	}
	if err := settingRepo.Update("Scrollback", req.Scrollback); err != nil {
		return err
	}
	if err := settingRepo.Update("ScrollSensitivity", req.ScrollSensitivity); err != nil {
		return err
	}
	return nil
}

func (u *SettingService) UpdatePassword(c *gin.Context, old, new string) error {
	if err := u.HandlePasswordExpired(c, old, new); err != nil {
		return err
	}
	_ = global.SESSION.Clean()
	return nil
}

func (u *SettingService) UpdateSystemSSL() error {
	certPath := path.Join(global.CONF.System.BaseDir, "1panel/secret/server.crt")
	keyPath := path.Join(global.CONF.System.BaseDir, "1panel/secret/server.key")
	certificate, err := os.ReadFile(certPath)
	if err != nil {
		return err
	}
	key, err := os.ReadFile(keyPath)
	if err != nil {
		return err
	}
	cert, err := tls.X509KeyPair(certificate, key)
	if err != nil {
		return err
	}
	constant.CertStore.Store(&cert)
	return nil
}

func loadInfoFromCert() (dto.SSLInfo, error) {
	var info dto.SSLInfo
	certFile := path.Join(global.CONF.System.BaseDir, "1panel/secret/server.crt")
	if _, err := os.Stat(certFile); err != nil {
		return info, err
	}
	certData, err := os.ReadFile(certFile)
	if err != nil {
		return info, err
	}
	certBlock, _ := pem.Decode(certData)
	if certBlock == nil {
		return info, err
	}
	certObj, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return info, err
	}
	var domains []string
	if len(certObj.IPAddresses) != 0 {
		for _, ip := range certObj.IPAddresses {
			domains = append(domains, ip.String())
		}
	}
	if len(certObj.DNSNames) != 0 {
		domains = append(domains, certObj.DNSNames...)
	}
	return dto.SSLInfo{
		Domain:   strings.Join(domains, ","),
		Timeout:  certObj.NotAfter.Format(constant.DateTimeLayout),
		RootPath: path.Join(global.CONF.System.BaseDir, "1panel/secret/server.crt"),
	}, nil
}

func checkCertValid() error {
	certificate, err := os.ReadFile(path.Join(global.CONF.System.BaseDir, "1panel/secret/server.crt.tmp"))
	if err != nil {
		return err
	}
	key, err := os.ReadFile(path.Join(global.CONF.System.BaseDir, "1panel/secret/server.key.tmp"))
	if err != nil {
		return err
	}
	if _, err = tls.X509KeyPair(certificate, key); err != nil {
		return err
	}
	certBlock, _ := pem.Decode(certificate)
	if certBlock == nil {
		return err
	}
	if _, err := x509.ParseCertificate(certBlock.Bytes); err != nil {
		return err
	}

	return nil
}

func loadPort(address string) string {
	re := regexp.MustCompile(`(?:(?:\[([0-9a-fA-F:]+)\])|([^:/\s]+))(?::(\d+))?$`)
	matches := re.FindStringSubmatch(address)
	if len(matches) <= 0 {
		return ""
	}
	var port string
	port = matches[3]
	if len(port) != 0 {
		return port
	}
	return ""
}
