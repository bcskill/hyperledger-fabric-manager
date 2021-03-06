package entity

import (
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"github.com/hyperledger/fabric/bccsp"
	"github.com/hyperledger/fabric/bccsp/factory"
	"github.com/hyperledger/fabric/bccsp/signer"
	"github.com/hyperledger/fabric/common/tools/cryptogen/ca"
	"github.com/hyperledger/fabric/common/tools/cryptogen/csp"
	"github.com/hyperledger/fabric/common/tools/cryptogen/msp"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/fabric-lab/hyperledger-fabric-manager/server/pkg/store"
)

type Organization struct {
	Country            string
	Province           string
	Locality           string
	Organization       string
	CommonName         string
	OrganizationalUnit string
	StreetAddress      string
	PostalCode         string
	PEMs               []PEM
	MSPs               []MSP
}

type PEM struct {
	Name string
	Key  string
	Cert string
	Type string
}

type MSP struct {
	Name string
	Path string
	Type string
	Role string
}

var (
	deliver = "./bin/deliver_stdout"
)

func (o *Organization) Create() error {

	o.generateRootCa()
	nodeName, mspPath, err := o.initMsp(msp.ORDERER)
	if err != nil {
		return err
	}
	o.MSPs = append(o.MSPs, MSP{Name: nodeName, Path: mspPath, Type: "orderer", Role: "orderer"})

	nodeName, mspPath, err = o.initMsp(msp.PEER)
	if err != nil {
		return err
	}
	o.MSPs = append(o.MSPs, MSP{Name: nodeName, Path: mspPath, Type: "peer", Role: "peer"})

	nodeName, mspPath, err = o.initAdminMsp()
	if err != nil {
		return err
	}
	o.MSPs = append(o.MSPs, MSP{Name: nodeName, Path: mspPath, Type: "peer", Role: "admin"})

	return nil
}

func (o *Organization) generateRootCa() error {
	os.RemoveAll(tempDir)

	// generate ROOT CA
	rootCA, err := ca.NewCA(tempDir, o.Organization, "ca."+o.CommonName, o.Country, o.Province, o.Locality, o.OrganizationalUnit, o.StreetAddress, o.PostalCode)
	if err != nil {
		return err
	}
	pem, _ := getPEM(rootCA.Name, "root")
	o.PEMs = append(o.PEMs, *pem)

	// generate TLS CA
	os.RemoveAll(tempDir)
	tlsCA, err := ca.NewCA(tempDir, o.Organization, "tlsca."+o.CommonName, o.Country, o.Province, o.Locality, o.OrganizationalUnit, o.StreetAddress, o.PostalCode)
	if err != nil {
		return err
	}
	pem, _ = getPEM(tlsCA.Name, "tls")
	o.PEMs = append(o.PEMs, *pem)
	// generate Admin CA
	o.generateAdminCa(rootCA)

	return nil
}

func (o *Organization) generateAdminCa(signCA *ca.CA) error {
	os.RemoveAll(tempDir)
	// generate private key
	priv, _, err := csp.GeneratePrivateKey(tempDir)
	if err != nil {
		return err
	}

	// get public key
	ecPubKey, err := csp.GetECPublicKey(priv)
	if err != nil {
		return err
	}

	var ous []string

	_, err = signCA.SignCertificate(tempDir,
		"admin@"+o.CommonName, ous, nil, ecPubKey, x509.KeyUsageDigitalSignature, []x509.ExtKeyUsage{})
	if err != nil {
		return err
	}
	pem, _ := getPEM("admin@"+o.CommonName, "admin")
	o.PEMs = append(o.PEMs, *pem)
	return nil
}

func (o *Organization) initMsp(nodeType int) (string, string, error) {
	var (
		node     string
		nodeName string
	)

	if nodeType == msp.ORDERER {
		node = "order"
		nodeName = "order." + o.CommonName
	} else {
		node = "peer"
		nodeName = "peer0." + o.CommonName
	}

	signCA, _, err := GetCA("ca."+o.CommonName, *o)
	if err != nil {
		return "", "", err
	}
	tlsCA, _, err := GetCA("tlsca."+o.CommonName, *o)
	if err != nil {
		return "", "", err
	}
	mspPath := Path(mspDir, o.CommonName, node+"s", nodeName)
	if err != nil {
		return "", "", err
	}
	os.RemoveAll(mspPath)
	err = msp.GenerateLocalMSP(mspPath, nodeName, []string{}, signCA, tlsCA, nodeType, false)
	if err != nil {
		return "", "", err
	}

	//copy admin cert
	adminCA, _, err := GetCA("admin@"+o.CommonName, *o)
	if err != nil {
		return "", "", err
	}
	adminPath := filepath.Join(mspPath, "msp", "admincerts")
	os.RemoveAll(adminPath)
	adminCertPath := filepath.Join(adminPath)
	os.Mkdir(adminCertPath, 0755)
	adminCertPath = filepath.Join(adminCertPath, adminCA.Name+"-cert.pem")
	pemExport(adminCertPath, "CERTIFICATE", adminCA.SignCert.Raw)
	return nodeName, mspPath, nil

}

func (o *Organization) initAdminMsp() (string, string, error) {
	node := "admin"
	nodeName := "admin." + o.CommonName
	signCA, _, err := GetCA("ca."+o.CommonName, *o)
	if err != nil {
		return "", "", err
	}
	tlsCA, _, err := GetCA("tlsca."+o.CommonName, *o)
	if err != nil {
		return "", "", err
	}
	mspPath := Path(mspDir, o.CommonName, node+"s", nodeName)
	if err != nil {
		return "", "", err
	}
	os.RemoveAll(mspPath)
	err = msp.GenerateLocalMSP(mspPath, nodeName, []string{}, signCA, tlsCA, msp.PEER, false)
	if err != nil {
		return "", "", err
	}

	//copy admin cert
	adminCA, key, err := GetCA("admin@"+o.CommonName, *o)
	if err != nil {
		return "", "", err
	}
	adminPath := filepath.Join(mspPath, "msp", "admincerts")
	os.RemoveAll(adminPath)
	adminCertPath := filepath.Join(adminPath)
	os.Mkdir(adminCertPath, 0755)
	adminCertPath = filepath.Join(adminCertPath, adminCA.Name+"-cert.pem")
	pemExport(adminCertPath, "CERTIFICATE", adminCA.SignCert.Raw)

	signPath := filepath.Join(mspPath, "msp", "signcerts")
	os.RemoveAll(signPath)
	signCertPath := filepath.Join(signPath)
	os.Mkdir(signCertPath, 0755)
	signCertPath = filepath.Join(signCertPath, nodeName+"-cert.pem")
	pemExport(signCertPath, "CERTIFICATE", adminCA.SignCert.Raw)

	//key
	keyPath := filepath.Join(mspPath, "msp", "keystore")
	writeAdminKey(key, keyPath)
	if err != nil {
		return "", "", err
	}
	return nodeName, mspPath, nil

}

func pemExport(path, pemType string, bytes []byte) error {
	//write pem out to file
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	return pem.Encode(file, &pem.Block{Type: pemType, Bytes: bytes})
}

func getPEM(name string, caType string) (*PEM, error) {
	pem := &PEM{Name: name, Type: caType}
	files, err := ioutil.ReadDir(tempDir)
	if err != nil {
		return pem, err
	}
	for _, f := range files {
		b, err := ioutil.ReadFile(filepath.Join(tempDir,f.Name()))
		if err != nil {
			return pem, err
		}
		if strings.Index(f.Name(), "cert.pem") != -1 {
			pem.Cert = string(b)
		} else {
			pem.Key = string(b)
		}
	}
	return pem, nil
}

func writeAdminKey(key string, path string) error {
	files, err := ioutil.ReadDir(path)
	if err != nil {
		return err
	}
	for _, f := range files {
		file, err := os.Create(filepath.Join(path, f.Name()))
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = file.WriteString(string(key))
		if err != nil {
			return err
		}
		return nil
	}
	return nil
}

func GetCA(commonName string, organization Organization) (*ca.CA, string, error) {
	for _, v := range organization.PEMs {
		if v.Name == commonName {
			cBlock, _ := pem.Decode([]byte(v.Cert))
			if cBlock == nil {
				return nil, "", errors.New("no PEM data found for certificate")
			}
			cert, err := x509.ParseCertificate(cBlock.Bytes)
			if err != nil {
				return nil, "", err
			}
			_, signer, err := LoadSigner(v.Key)
			if err != nil {
				return nil, "", err
			}
			ca := &ca.CA{
				Name:               commonName,
				Signer:             signer,
				SignCert:           cert,
				Country:            organization.Country,
				Province:           organization.Province,
				Locality:           organization.Locality,
				OrganizationalUnit: organization.OrganizationalUnit,
				StreetAddress:      organization.StreetAddress,
				PostalCode:         organization.PostalCode,
			}

			return ca, v.Key, nil
		}
	}
	return nil, "", nil
}

func getCAName(caType string, commonName string) string {
	if caType == "ca" {
		return "ca." + commonName
	} else if caType == "tls" {
		return "tlsca." + commonName
	}
	return "ca." + commonName
}

func LoadSigner(rawKey string) (bccsp.Key, crypto.Signer, error) {
	var err error
	var priv bccsp.Key
	var s crypto.Signer

	opts := &factory.FactoryOpts{
		ProviderName: "SW",
		SwOpts: &factory.SwOpts{
			HashFamily: "SHA2",
			SecLevel:   256,
		},
	}

	csp, err := factory.GetBCCSPFromOpts(opts)
	if err != nil {
		return nil, nil, err
	}

	block, _ := pem.Decode([]byte(rawKey))
	priv, err = csp.KeyImport(block.Bytes, &bccsp.ECDSAPrivateKeyImportOpts{Temporary: true})
	if err != nil {
		return nil, nil, err
	}

	s, err = signer.New(csp, priv)
	if err != nil {
		return nil, nil, err
	}

	return priv, s, err
}

func getMspByName(mspName string) (MSP, error) {

	var m MSP
	records, err := store.Bt.View(organizations)
	if err != nil {
		return m, err
	}
	for _, v := range records {
		i := MapToEntity(v, organizations)
		if o, ok := i.(*Organization); ok {
			for _, m := range o.MSPs {
				if mspName == m.Name {
					return m, nil
				}
			}
		}

	}
	return m, errors.New("Not find Msp")
}

func getOrgByName(oName string) (*Organization, error) {
	var o *Organization
	i, err := store.Bt.ViewByKey(organizations, oName)
	if err != nil {
		return o, err
	}
	i = MapToEntity(i, organizations)
	if o, ok := i.(*Organization); ok {
		return o, nil
	}
	return o, nil
}
