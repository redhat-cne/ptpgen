package config

import (
	"fmt"
	"strings"

	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
	"github.com/redhat-cne/ptpgen/pkg/discovery"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// PTP mode the user wants to configure.
type Mode string

const (
	ModeOC           Mode = "oc"
	ModeBC           Mode = "bc"
	ModeDualNicBC    Mode = "dualnicbc"
	ModeDualNicBCHA  Mode = "dualnicbcha"
	ModeTGM          Mode = "tgm"
	ModeDualFollower Mode = "dualfollower"
)

// Options controls config generation behavior.
type Options struct {
	Mode         Mode
	ExternalGM   bool
	FIFO         bool
	Auth         bool
	Namespace    string
	Domain       int
	WPCIfList    []string // for TGM mode: list of WPC-enabled interfaces
	WPCDeviceID  string   // for TGM mode: GNSS device ID
}

// Policy and label names matching ptp-operator test conventions.
const (
	PtpGrandMasterPolicyName    = "test-grandmaster"
	PtpWPCGrandMasterPolicyName = "test-wpc-grandmaster"
	PtpBcMaster1PolicyName      = "test-bc-master1"
	PtpBcMaster2PolicyName      = "test-bc-master2"
	PtpSlave1PolicyName         = "test-slave1"
	PtpSlave2PolicyName         = "test-slave2"
	PtpDualNicBCHAPolicyName    = "test-dual-nic-bc-ha"

	PtpGrandmasterNodeLabel    = "ptp/test-grandmaster"
	PtpClockUnderTestNodeLabel = "ptp/clock-under-test"
	PtpSlave1NodeLabel         = "ptp/test-slave1"
	PtpSlave2NodeLabel         = "ptp/test-slave2"
)

const (
	ptp4lEthernet      = "-2 --summary_interval -4"
	ptp4lEthernetSlave = "-2 -s --summary_interval -4"
	phc2sysGM          = "-a -r -r -n 24"
	phc2sysSlave       = "-a -r -n 24 -m -N 8 -R 16"
	phc2sysDualNicBCHA = "-a -r -m -l 7 -n 24 "
	schedOther         = "SCHED_OTHER"
	schedFIFO          = "SCHED_FIFO"
)

// GenerateResult holds the generated PtpConfigs and the node label
// assignments needed for --apply mode.
type GenerateResult struct {
	Configs    []ptpv1.PtpConfig
	NodeLabels map[string]string // nodeName -> label
}

// Generate produces a list of PtpConfig objects based on the discovered
// topology and the user's chosen mode.
func Generate(result *discovery.Result, opts Options) (*GenerateResult, error) {
	b := &builder{
		result:     result,
		opts:       opts,
		ns:         opts.Namespace,
		nodeLabels: make(map[string]string),
	}
	if b.ns == "" {
		b.ns = "openshift-ptp"
	}
	b.schedPolicy = schedOther
	if opts.FIFO {
		b.schedPolicy = schedFIFO
	}

	var configs []ptpv1.PtpConfig
	var err error
	switch opts.Mode {
	case ModeOC:
		configs, err = b.generateOC()
	case ModeBC:
		configs, err = b.generateBC()
	case ModeDualNicBC:
		configs, err = b.generateDualNicBC(false)
	case ModeDualNicBCHA:
		configs, err = b.generateDualNicBC(true)
	case ModeTGM:
		configs, err = b.generateTGM()
	case ModeDualFollower:
		configs, err = b.generateDualFollower()
	default:
		return nil, fmt.Errorf("unsupported mode: %s", opts.Mode)
	}
	if err != nil {
		return nil, err
	}
	return &GenerateResult{Configs: configs, NodeLabels: b.nodeLabels}, nil
}

type builder struct {
	result      *discovery.Result
	opts        Options
	ns          string
	schedPolicy string
	nodeLabels  map[string]string // tracks node -> label for apply mode
}

// --- Orchestrators ---

func (b *builder) generateOC() ([]ptpv1.PtpConfig, error) {
	var configs []ptpv1.PtpConfig
	useExtGM := b.opts.ExternalGM

	if !useExtGM {
		if !b.result.HasSolution(discovery.AlgoOC) && b.result.HasSolution(discovery.AlgoOCExtGM) {
			logrus.Info("No internal GM solution found, auto-detecting external GM")
			useExtGM = true
		}
	}

	if useExtGM {
		algo := discovery.AlgoOCExtGM
		if !b.result.HasSolution(algo) {
			return nil, fmt.Errorf("no solution for OC (tried both internal and external GM)")
		}
		slave1, _ := b.result.GetPort(algo, discovery.Slave1)
		b.nodeLabels[slave1.NodeName] = PtpClockUnderTestNodeLabel
		configs = append(configs, b.makeOC(PtpSlave1PolicyName, slave1, true, PtpClockUnderTestNodeLabel))
	} else {
		algo := discovery.AlgoOC
		gm, _ := b.result.GetPort(algo, discovery.Grandmaster)
		slave1, _ := b.result.GetPort(algo, discovery.Slave1)
		b.nodeLabels[gm.NodeName] = PtpGrandmasterNodeLabel
		b.nodeLabels[slave1.NodeName] = PtpClockUnderTestNodeLabel
		configs = append(configs, b.makeGM(gm))
		configs = append(configs, b.makeOC(PtpSlave1PolicyName, slave1, true, PtpClockUnderTestNodeLabel))
	}
	return configs, nil
}

func (b *builder) generateBC() ([]ptpv1.PtpConfig, error) {
	var configs []ptpv1.PtpConfig
	useExtGM := b.opts.ExternalGM

	if !useExtGM {
		hasInternal := b.result.HasSolution(discovery.AlgoBC) || b.result.HasSolution(discovery.AlgoBCWithSlaves)
		hasExternal := b.result.HasSolution(discovery.AlgoBCExtGM) || b.result.HasSolution(discovery.AlgoBCWithSlavesExtGM)
		if !hasInternal && hasExternal {
			logrus.Info("No internal GM solution found, auto-detecting external GM")
			useExtGM = true
		}
	}

	if useExtGM {
		bestAlgo := ""
		if b.result.HasSolution(discovery.AlgoBCExtGM) {
			bestAlgo = discovery.AlgoBCExtGM
		}
		if b.result.HasSolution(discovery.AlgoBCWithSlavesExtGM) {
			bestAlgo = discovery.AlgoBCWithSlavesExtGM
		}
		if bestAlgo == "" {
			return nil, fmt.Errorf("no solution for BC (tried both internal and external GM)")
		}

		bc1Master, _ := b.result.GetPort(bestAlgo, discovery.BC1Master)
		bc1Slave, _ := b.result.GetPort(bestAlgo, discovery.BC1Slave)
		b.nodeLabels[bc1Master.NodeName] = PtpClockUnderTestNodeLabel
		configs = append(configs, b.makeBC(PtpBcMaster1PolicyName, bc1Master, bc1Slave, true))

		if bestAlgo == discovery.AlgoBCWithSlavesExtGM {
			slave1, _ := b.result.GetPort(bestAlgo, discovery.Slave1)
			b.nodeLabels[slave1.NodeName] = PtpSlave1NodeLabel
			configs = append(configs, b.makeOC(PtpSlave1PolicyName, slave1, false, PtpSlave1NodeLabel))
		}
	} else {
		bestAlgo := ""
		if b.result.HasSolution(discovery.AlgoBC) {
			bestAlgo = discovery.AlgoBC
		}
		if b.result.HasSolution(discovery.AlgoBCWithSlaves) {
			bestAlgo = discovery.AlgoBCWithSlaves
		}
		if bestAlgo == "" {
			return nil, fmt.Errorf("no solution for BC")
		}

		gm, _ := b.result.GetPort(bestAlgo, discovery.Grandmaster)
		bc1Master, _ := b.result.GetPort(bestAlgo, discovery.BC1Master)
		bc1Slave, _ := b.result.GetPort(bestAlgo, discovery.BC1Slave)
		b.nodeLabels[gm.NodeName] = PtpGrandmasterNodeLabel
		b.nodeLabels[bc1Master.NodeName] = PtpClockUnderTestNodeLabel
		configs = append(configs, b.makeGM(gm))
		configs = append(configs, b.makeBC(PtpBcMaster1PolicyName, bc1Master, bc1Slave, true))

		if bestAlgo == discovery.AlgoBCWithSlaves {
			slave1, _ := b.result.GetPort(bestAlgo, discovery.Slave1)
			b.nodeLabels[slave1.NodeName] = PtpSlave1NodeLabel
			configs = append(configs, b.makeOC(PtpSlave1PolicyName, slave1, false, PtpSlave1NodeLabel))
		}
	}
	return configs, nil
}

func (b *builder) generateDualNicBC(haEnabled bool) ([]ptpv1.PtpConfig, error) {
	var configs []ptpv1.PtpConfig
	useExtGM := b.opts.ExternalGM

	if !useExtGM {
		hasInternal := b.result.HasSolution(discovery.AlgoDualNicBC) || b.result.HasSolution(discovery.AlgoDualNicBCWithSlaves)
		hasExternal := b.result.HasSolution(discovery.AlgoDualNicBCExtGM) || b.result.HasSolution(discovery.AlgoDualNicBCWithSlavesExtGM)
		if !hasInternal && hasExternal {
			logrus.Info("No internal GM solution found, auto-detecting external GM")
			useExtGM = true
		}
	}

	bestAlgo := ""
	if useExtGM {
		if b.result.HasSolution(discovery.AlgoDualNicBCExtGM) {
			bestAlgo = discovery.AlgoDualNicBCExtGM
		}
		if b.result.HasSolution(discovery.AlgoDualNicBCWithSlavesExtGM) {
			bestAlgo = discovery.AlgoDualNicBCWithSlavesExtGM
		}
	} else {
		if b.result.HasSolution(discovery.AlgoDualNicBC) {
			bestAlgo = discovery.AlgoDualNicBC
		}
		if b.result.HasSolution(discovery.AlgoDualNicBCWithSlaves) {
			bestAlgo = discovery.AlgoDualNicBCWithSlaves
		}
	}
	if bestAlgo == "" {
		return nil, fmt.Errorf("no solution for DualNicBC (tried both internal and external GM)")
	}

	// GM (only for non-external)
	if !useExtGM {
		switch bestAlgo {
		case discovery.AlgoDualNicBC, discovery.AlgoDualNicBCWithSlaves:
			gm, _ := b.result.GetPort(bestAlgo, discovery.Grandmaster)
			b.nodeLabels[gm.NodeName] = PtpGrandmasterNodeLabel
			configs = append(configs, b.makeGM(gm))
		}
	}

	// BC1
	bc1Master, _ := b.result.GetPort(bestAlgo, discovery.BC1Master)
	bc1Slave, _ := b.result.GetPort(bestAlgo, discovery.BC1Slave)
	b.nodeLabels[bc1Master.NodeName] = PtpClockUnderTestNodeLabel
	configs = append(configs, b.makeBC(PtpBcMaster1PolicyName, bc1Master, bc1Slave, !haEnabled))

	// BC2
	bc2Master, _ := b.result.GetPort(bestAlgo, discovery.BC2Master)
	bc2Slave, _ := b.result.GetPort(bestAlgo, discovery.BC2Slave)
	configs = append(configs, b.makeBC(PtpBcMaster2PolicyName, bc2Master, bc2Slave, false))

	// Slaves (if available)
	switch bestAlgo {
	case discovery.AlgoDualNicBCWithSlaves, discovery.AlgoDualNicBCWithSlavesExtGM:
		slave1, _ := b.result.GetPort(bestAlgo, discovery.Slave1)
		slave2, _ := b.result.GetPort(bestAlgo, discovery.Slave2)
		b.nodeLabels[slave1.NodeName] = PtpSlave1NodeLabel
		b.nodeLabels[slave2.NodeName] = PtpSlave2NodeLabel
		configs = append(configs, b.makeOC(PtpSlave1PolicyName, slave1, false, PtpSlave1NodeLabel))
		configs = append(configs, b.makeOC(PtpSlave2PolicyName, slave2, false, PtpSlave2NodeLabel))
	}

	// HA config
	if haEnabled {
		configs = append(configs, b.makePhc2SysHA(PtpDualNicBCHAPolicyName, bc1Master.NodeName,
			[]string{PtpBcMaster1PolicyName, PtpBcMaster2PolicyName}))
	}

	return configs, nil
}

func (b *builder) generateTGM() ([]ptpv1.PtpConfig, error) {
	algo := discovery.AlgoTelcoGM
	if !b.result.HasSolution(algo) {
		return nil, fmt.Errorf("no solution for TelcoGM")
	}
	gm, _ := b.result.GetPort(algo, discovery.Grandmaster)

	if len(b.opts.WPCIfList) == 0 {
		return nil, fmt.Errorf("TGM mode requires --wpc-interfaces")
	}

	b.nodeLabels[gm.NodeName] = PtpClockUnderTestNodeLabel
	configs := []ptpv1.PtpConfig{
		b.makeWPCGM(PtpWPCGrandMasterPolicyName, gm, b.opts.WPCIfList, b.opts.WPCDeviceID),
	}
	return configs, nil
}

func (b *builder) generateDualFollower() ([]ptpv1.PtpConfig, error) {
	var configs []ptpv1.PtpConfig
	useExtGM := b.opts.ExternalGM

	if !useExtGM {
		if !b.result.HasSolution(discovery.AlgoDualFollower) && b.result.HasSolution(discovery.AlgoDualFollowerExtGM) {
			logrus.Info("No internal GM solution found, auto-detecting external GM")
			useExtGM = true
		}
	}

	if useExtGM {
		algo := discovery.AlgoDualFollowerExtGM
		if !b.result.HasSolution(algo) {
			return nil, fmt.Errorf("no solution for DualFollower (tried both internal and external GM)")
		}
		slave1, _ := b.result.GetPort(algo, discovery.Slave1)
		slave2, _ := b.result.GetPort(algo, discovery.Slave2)
		b.nodeLabels[slave1.NodeName] = PtpClockUnderTestNodeLabel
		configs = append(configs, b.makeDualFollower(PtpSlave1PolicyName, slave1, slave2, true, PtpClockUnderTestNodeLabel))
	} else {
		algo := discovery.AlgoDualFollower
		gm, _ := b.result.GetPort(algo, discovery.Grandmaster)
		slave1, _ := b.result.GetPort(algo, discovery.Slave1)
		slave2, _ := b.result.GetPort(algo, discovery.Slave2)
		b.nodeLabels[gm.NodeName] = PtpGrandmasterNodeLabel
		b.nodeLabels[slave1.NodeName] = PtpClockUnderTestNodeLabel
		configs = append(configs, b.makeGM(gm))
		configs = append(configs, b.makeDualFollower(PtpSlave1PolicyName, slave1, slave2, true, PtpClockUnderTestNodeLabel))
	}
	return configs, nil
}

// --- Config builders ---

func (b *builder) makeGM(port discovery.PortInfo) ptpv1.PtpConfig {
	ptp4lConfig := b.basePtp4lConfig() + "\npriority1 0\npriority2 0\nclockClass 6"
	ptp4lConfig = b.addAuthSettings(addInterface(ptp4lConfig, port.IfName, 1))

	ptp4lOpts := ptp4lEthernet
	phc2sys := phc2sysGM
	return b.makeConfig(PtpGrandMasterPolicyName, &port.IfName, &ptp4lOpts, ptp4lConfig,
		&phc2sys, PtpGrandmasterNodeLabel)
}

func (b *builder) makeOC(policyName string, port discovery.PortInfo, phc2sys bool, label string) ptpv1.PtpConfig {
	ptp4lConfig := b.basePtp4lConfig()
	ptp4lConfig = b.addAuthSettings(addInterface(ptp4lConfig, port.IfName, 0))

	ptp4lOpts := ptp4lEthernetSlave
	var phc2sysOpts *string
	if phc2sys {
		s := phc2sysSlave
		phc2sysOpts = &s
	}
	return b.makeConfig(policyName, &port.IfName, &ptp4lOpts, ptp4lConfig, phc2sysOpts, label)
}

func (b *builder) makeBC(policyName string, masterPort, slavePort discovery.PortInfo, phc2sys bool) ptpv1.PtpConfig {
	ptp4lConfig := b.basePtp4lConfig() + "\nboundary_clock_jbod 1\ngmCapable 0"
	ptp4lConfig = b.addAuthSettings(addInterface(ptp4lConfig, slavePort.IfName, 0))
	ptp4lConfig = b.addAuthSettings(addInterface(ptp4lConfig, masterPort.IfName, 1))

	ptp4lOpts := ptp4lEthernet
	var phc2sysOpts *string
	if phc2sys {
		s := phc2sysSlave
		phc2sysOpts = &s
	}
	return b.makeConfig(policyName, nil, &ptp4lOpts, ptp4lConfig, phc2sysOpts, PtpClockUnderTestNodeLabel)
}

func (b *builder) makeDualFollower(policyName string, slave1, slave2 discovery.PortInfo, phc2sys bool, label string) ptpv1.PtpConfig {
	ptp4lConfig := b.basePtp4lConfig() + "\nslaveOnly 1"
	ptp4lConfig = b.addAuthSettings(addInterface(ptp4lConfig, slave1.IfName, 0))
	ptp4lConfig = b.addAuthSettings(addInterface(ptp4lConfig, slave2.IfName, 0))

	ptp4lOpts := ptp4lEthernetSlave
	var phc2sysOpts *string
	if phc2sys {
		s := phc2sysSlave
		phc2sysOpts = &s
	}
	return b.makeConfig(policyName, nil, &ptp4lOpts, ptp4lConfig, phc2sysOpts, label)
}

func (b *builder) makeWPCGM(policyName string, port discovery.PortInfo, ifList []string, deviceID string) ptpv1.PtpConfig {
	ts2phcConfig := BaseTs2PhcConfig + fmt.Sprintf("\nts2phc.nmea_serialport  /dev/%s\n", deviceID)
	ts2phcConfig = fmt.Sprintf("%s\n[%s]\nts2phc.extts_polarity rising\nts2phc.extts_correction 0\n", ts2phcConfig, ifList[0])

	ptp4lConfig := b.basePtp4lConfig() + "boundary_clock_jbod 1\n"
	ptp4lConfig = b.addAuthSettings(addInterface(ptp4lConfig, ifList[0], 1))
	if len(ifList) > 1 {
		ptp4lConfig = b.addAuthSettings(addInterface(ptp4lConfig, ifList[1], 1))
	}

	ptp4lOpts := ptp4lEthernet
	ts2phcOpts := " "
	ph2sysOpts := fmt.Sprintf("-r -u 0 -m -N 8 -R 16 -s %s -n 24", ifList[0])

	profile := ptpv1.PtpProfile{
		Name:                  &policyName,
		Phc2sysOpts:           &ph2sysOpts,
		Ptp4lOpts:             &ptp4lOpts,
		Ptp4lConf:             &ptp4lConfig,
		Ts2PhcConf:            &ts2phcConfig,
		Ts2PhcOpts:            &ts2phcOpts,
		PtpSchedulingPolicy:   &b.schedPolicy,
		PtpSchedulingPriority: ptr.To(int64(65)),
		PtpClockThreshold:     &ptpv1.PtpClockThreshold{},
		PtpSettings:           map[string]string{"logReduce": "false"},
	}

	label := PtpClockUnderTestNodeLabel
	return ptpv1.PtpConfig{
		TypeMeta:   metav1.TypeMeta{Kind: "PtpConfig", APIVersion: "ptp.openshift.io/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: policyName, Namespace: b.ns},
		Spec: ptpv1.PtpConfigSpec{
			Profile:   []ptpv1.PtpProfile{profile},
			Recommend: []ptpv1.PtpRecommend{{Profile: &policyName, Priority: ptr.To(int64(5)), Match: []ptpv1.MatchRule{{NodeLabel: &label}}}},
		},
	}
}

func (b *builder) makePhc2SysHA(policyName, nodeName string, haProfiles []string) ptpv1.PtpConfig {
	phc2sysOpts := phc2sysDualNicBCHA
	ptp4lOpts := ""
	label := PtpClockUnderTestNodeLabel

	profile := ptpv1.PtpProfile{
		Name:                  &policyName,
		Phc2sysOpts:           &phc2sysOpts,
		Ptp4lOpts:             &ptp4lOpts,
		PtpSchedulingPolicy:   &b.schedPolicy,
		PtpSchedulingPriority: ptr.To(int64(65)),
		PtpSettings:           map[string]string{"haProfiles": strings.Join(haProfiles, ",")},
	}

	return ptpv1.PtpConfig{
		TypeMeta:   metav1.TypeMeta{Kind: "PtpConfig", APIVersion: "ptp.openshift.io/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: policyName, Namespace: b.ns},
		Spec: ptpv1.PtpConfigSpec{
			Profile:   []ptpv1.PtpProfile{profile},
			Recommend: []ptpv1.PtpRecommend{{Profile: &policyName, Priority: ptr.To(int64(5)), Match: []ptpv1.MatchRule{{NodeLabel: &label}}}},
		},
	}
}

// --- Helpers ---

func (b *builder) makeConfig(profileName string, ifaceName *string, ptp4lOpts *string, ptp4lConfig string, phc2sysOpts *string, nodeLabel string) ptpv1.PtpConfig {
	profile := ptpv1.PtpProfile{
		Name:                  &profileName,
		Interface:             ifaceName,
		Phc2sysOpts:           phc2sysOpts,
		Ptp4lOpts:             ptp4lOpts,
		PtpSchedulingPolicy:   &b.schedPolicy,
		PtpSchedulingPriority: ptr.To(int64(65)),
		PtpClockThreshold:     &ptpv1.PtpClockThreshold{},
	}
	if ptp4lConfig != "" {
		profile.Ptp4lConf = &ptp4lConfig
	}

	return ptpv1.PtpConfig{
		TypeMeta:   metav1.TypeMeta{Kind: "PtpConfig", APIVersion: "ptp.openshift.io/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: profileName, Namespace: b.ns},
		Spec: ptpv1.PtpConfigSpec{
			Profile:   []ptpv1.PtpProfile{profile},
			Recommend: []ptpv1.PtpRecommend{{Profile: &profileName, Priority: ptr.To(int64(5)), Match: []ptpv1.MatchRule{{NodeLabel: &nodeLabel}}}},
		},
	}
}

func addInterface(ptpConfig, iface string, masterOnly int) string {
	return fmt.Sprintf("%s\n[%s]\nmasterOnly %d", ptpConfig, iface, masterOnly)
}

func (b *builder) addAuthSettings(ptpConfig string) string {
	if b.opts.Auth {
		return ptpConfig + "\nspp 1\nactive_key_id 1"
	}
	return ptpConfig
}

func (b *builder) basePtp4lConfig() string {
	cfg := BasePtp4lConfig
	if b.opts.Domain != 0 {
		cfg = strings.Replace(cfg, "domainNumber 24", fmt.Sprintf("domainNumber %d", b.opts.Domain), 1)
	}
	if b.opts.Auth {
		cfg = addAuthGlobal(cfg)
	}
	return cfg
}

func addAuthGlobal(baseConfig string) string {
	return strings.Replace(baseConfig, "[global]\n",
		"[global]\nsa_file /etc/ptp-secret-mount/ptp-security-conf/ptp-security.conf\nspp -1\n", 1)
}
