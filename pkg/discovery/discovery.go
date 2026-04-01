package discovery

import (
	"fmt"

	l2lib "github.com/redhat-cne/l2discovery-lib"
	solver "github.com/redhat-cne/l2discovery-lib/pkg/graphsolver"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const L2DiscoveryImage = "quay.io/redhat-cne/l2discovery:v15"

// Clock role indices used in solver solutions.
type ClockRole int

const NumClockRoles = 7

const (
	Grandmaster ClockRole = iota
	Slave1
	Slave2
	BC1Master
	BC1Slave
	BC2Master
	BC2Slave
)

// Algorithm names matching ptp-operator test constants.
const (
	AlgoOC                       = "OC"
	AlgoDualFollower             = "DualFollower"
	AlgoBC                       = "BC"
	AlgoBCWithSlaves             = "BCWithSlaves"
	AlgoDualNicBC                = "DualNicBC"
	AlgoTelcoGM                  = "TGM"
	AlgoDualNicBCWithSlaves      = "DualNicBCWithSlaves"
	AlgoOCExtGM                  = "OCExtGM"
	AlgoDualFollowerExtGM        = "DualFollowerExtGM"
	AlgoBCExtGM                  = "BCExtGM"
	AlgoDualNicBCExtGM           = "DualNicBCExtGM"
	AlgoBCWithSlavesExtGM        = "BCWithSlavesExtGM"
	AlgoDualNicBCWithSlavesExtGM = "DualNicBCWithSlavesExtGM"
)

const FirstSolution = 0

var enabledProblems = []string{
	AlgoOC, AlgoBC, AlgoBCWithSlaves,
	AlgoDualNicBC, AlgoDualNicBCWithSlaves,
	AlgoTelcoGM,
	AlgoOCExtGM, AlgoBCExtGM, AlgoBCWithSlavesExtGM,
	AlgoDualNicBCExtGM, AlgoDualNicBCWithSlavesExtGM,
	AlgoDualFollower, AlgoDualFollowerExtGM,
}

// PortInfo holds the node and interface name for a discovered port.
type PortInfo struct {
	NodeName string
	IfName   string
}

// Result holds the solver solutions and L2 config.
type Result struct {
	L2Config              l2lib.L2Info
	Solutions             map[string]*[][]int
	ClockRolesAlgoMapping map[string]*[]int
}

// GetPort returns the PortInfo for a given algorithm solution and clock role.
func (r *Result) GetPort(algo string, role ClockRole) (PortInfo, error) {
	sols, ok := r.Solutions[algo]
	if !ok || len(*sols) == 0 {
		return PortInfo{}, fmt.Errorf("no solution for algorithm %s", algo)
	}
	roleIdx := (*r.ClockRolesAlgoMapping[algo])[int(role)]
	portIdx := (*sols)[FirstSolution][roleIdx]
	ifList := r.L2Config.GetPtpIfList()
	if portIdx >= len(ifList) {
		return PortInfo{}, fmt.Errorf("port index %d out of range", portIdx)
	}
	p := ifList[portIdx]
	return PortInfo{NodeName: p.NodeName, IfName: p.IfName}, nil
}

// HasSolution returns true if the given algorithm has at least one solution.
func (r *Result) HasSolution(algo string) bool {
	sols, ok := r.Solutions[algo]
	return ok && len(*sols) > 0
}

// Discover runs L2 discovery and the constraint solver, returning the results.
func Discover(k8sClient kubernetes.Interface, restConfig *rest.Config, useContainerCmds bool) (*Result, error) {
	l2lib.GlobalL2DiscoveryConfig.SetL2Client(k8sClient, restConfig)

	config, err := l2lib.GlobalL2DiscoveryConfig.GetL2DiscoveryConfig(true, false, useContainerCmds, L2DiscoveryImage)
	if err != nil {
		return nil, fmt.Errorf("L2 discovery failed: %w", err)
	}
	logrus.Infof("L2 discovery complete, %d PTP-capable interfaces found", len(config.GetPtpIfList()))

	solver.GlobalConfig.SetL2Config(config)
	initAndSolveProblems()

	solutions := solver.GlobalConfig.GetSolutions()
	if len(solutions) == 0 {
		return nil, fmt.Errorf("no solutions found for any PTP topology")
	}

	return &Result{
		L2Config:              config,
		Solutions:             solutions,
		ClockRolesAlgoMapping: clockRolesAlgoMapping,
	}, nil
}

// package-level mapping built by initAndSolveProblems
var clockRolesAlgoMapping map[string]*[]int

func initAndSolveProblems() {
	problems := make(map[string]*[][][]int)
	clockRolesAlgoMapping = make(map[string]*[]int)

	// OC: grandmaster(1) + slave(0), different LANs
	problems[AlgoOC] = &[][][]int{
		{{int(solver.StepNil), 0, 0}},
		{{int(solver.StepSameLan2), 2, 0, 1}},
	}

	// DualFollower: slave1(0) + slave2(1) on same node, grandmaster(2) on same LAN
	problems[AlgoDualFollower] = &[][][]int{
		{{int(solver.StepNil), 0, 0}},
		{{int(solver.StepSameNode), 2, 0, 1}},
		{{int(solver.StepSameLan2), 2, 0, 2}},
	}

	// BC: bc1slave(0) + bc1master(1) on same NIC, grandmaster(2) on same LAN as bc1slave
	problems[AlgoBC] = &[][][]int{
		{{int(solver.StepNil), 0, 0}},
		{{int(solver.StepSameNic), 2, 0, 1}},
		{{int(solver.StepSameLan2), 2, 0, 2}},
	}

	// BCWithSlaves: slave1(0) on same LAN as bc1master(1), bc1slave(2) same NIC as bc1master, gm(3) same LAN as bc1slave
	problems[AlgoBCWithSlaves] = &[][][]int{
		{{int(solver.StepNil), 0, 0}},
		{{int(solver.StepSameLan2), 2, 0, 1}},
		{{int(solver.StepSameNic), 2, 1, 2}},
		{{int(solver.StepSameLan2), 2, 2, 3}},
	}

	// DualNicBC
	problems[AlgoDualNicBC] = &[][][]int{
		{{int(solver.StepNil), 0, 0}},
		{{int(solver.StepSameNic), 2, 0, 1}},
		{{int(solver.StepSameLan2), 2, 1, 2}},
		{{int(solver.StepSameNode), 2, 1, 3},
			{int(solver.StepSameLan2), 2, 2, 3}},
		{{int(solver.StepSameNic), 2, 3, 4},
			{int(solver.StepSameNic), 2, 1, 3, solver.Negative}},
	}

	// DualNicBCWithSlaves
	problems[AlgoDualNicBCWithSlaves] = &[][][]int{
		{{int(solver.StepNil), 0, 0}},
		{{int(solver.StepSameLan2), 2, 0, 1}},
		{{int(solver.StepSameNic), 2, 1, 2}},
		{{int(solver.StepSameLan2), 2, 2, 3}},
		{{int(solver.StepSameNode), 2, 2, 4},
			{int(solver.StepSameLan2), 2, 3, 4}},
		{{int(solver.StepSameNic), 2, 4, 5},
			{int(solver.StepSameNic), 2, 2, 4, solver.Negative}},
		{{int(solver.StepSameLan2), 2, 5, 6}},
	}

	// TelcoGM
	problems[AlgoTelcoGM] = &[][][]int{
		{{int(solver.StepIsWPCNic), 0, 0}},
	}

	// External GM variants
	problems[AlgoOCExtGM] = &[][][]int{
		{{int(solver.StepIsPTP), 0, 0}},
	}

	problems[AlgoDualFollowerExtGM] = &[][][]int{
		{{int(solver.StepIsPTP), 0, 0}},
		{{int(solver.StepSameNode), 2, 0, 1},
			{int(solver.StepIsPTP), 0, 1}},
	}

	problems[AlgoBCExtGM] = &[][][]int{
		{{int(solver.StepIsPTP), 0, 0}},
		{{int(solver.StepSameNic), 2, 0, 1}},
	}

	problems[AlgoBCWithSlavesExtGM] = &[][][]int{
		{{int(solver.StepIsPTP), 0, 0}},
		{{int(solver.StepSameNic), 2, 0, 1}},
		{{int(solver.StepSameLan2), 2, 1, 2}},
	}

	problems[AlgoDualNicBCExtGM] = &[][][]int{
		{{int(solver.StepIsPTP), 0, 0}},
		{{int(solver.StepSameNic), 2, 0, 1}},
		{{int(solver.StepSameNode), 2, 0, 2},
			{int(solver.StepIsPTP), 0, 2}},
		{{int(solver.StepSameNic), 2, 2, 3},
			{int(solver.StepSameNic), 2, 0, 2, solver.Negative}},
	}

	problems[AlgoDualNicBCWithSlavesExtGM] = &[][][]int{
		{{int(solver.StepIsPTP), 0, 0}},
		{{int(solver.StepSameNic), 2, 0, 1}},
		{{int(solver.StepSameNode), 2, 0, 2},
			{int(solver.StepIsPTP), 0, 2}},
		{{int(solver.StepSameNic), 2, 2, 3},
			{int(solver.StepSameNic), 2, 0, 2, solver.Negative}},
		{{int(solver.StepSameLan2), 2, 1, 4}},
		{{int(solver.StepSameLan2), 2, 3, 5}},
	}

	// Initialize role mappings
	for _, name := range enabledProblems {
		alloc := make([]int, NumClockRoles)
		clockRolesAlgoMapping[name] = &alloc
	}

	// OC
	(*clockRolesAlgoMapping[AlgoOC])[Slave1] = 0
	(*clockRolesAlgoMapping[AlgoOC])[Grandmaster] = 1

	// DualFollower
	(*clockRolesAlgoMapping[AlgoDualFollower])[Slave1] = 0
	(*clockRolesAlgoMapping[AlgoDualFollower])[Slave2] = 1
	(*clockRolesAlgoMapping[AlgoDualFollower])[Grandmaster] = 2

	// BC
	(*clockRolesAlgoMapping[AlgoBC])[BC1Slave] = 0
	(*clockRolesAlgoMapping[AlgoBC])[BC1Master] = 1
	(*clockRolesAlgoMapping[AlgoBC])[Grandmaster] = 2

	// BCWithSlaves
	(*clockRolesAlgoMapping[AlgoBCWithSlaves])[Slave1] = 0
	(*clockRolesAlgoMapping[AlgoBCWithSlaves])[BC1Master] = 1
	(*clockRolesAlgoMapping[AlgoBCWithSlaves])[BC1Slave] = 2
	(*clockRolesAlgoMapping[AlgoBCWithSlaves])[Grandmaster] = 3

	// DualNicBC
	(*clockRolesAlgoMapping[AlgoDualNicBC])[BC1Slave] = 0
	(*clockRolesAlgoMapping[AlgoDualNicBC])[BC1Master] = 1
	(*clockRolesAlgoMapping[AlgoDualNicBC])[Grandmaster] = 2
	(*clockRolesAlgoMapping[AlgoDualNicBC])[BC2Slave] = 3
	(*clockRolesAlgoMapping[AlgoDualNicBC])[BC2Master] = 4

	// DualNicBCWithSlaves
	(*clockRolesAlgoMapping[AlgoDualNicBCWithSlaves])[Slave1] = 0
	(*clockRolesAlgoMapping[AlgoDualNicBCWithSlaves])[BC1Master] = 1
	(*clockRolesAlgoMapping[AlgoDualNicBCWithSlaves])[BC1Slave] = 2
	(*clockRolesAlgoMapping[AlgoDualNicBCWithSlaves])[Grandmaster] = 3
	(*clockRolesAlgoMapping[AlgoDualNicBCWithSlaves])[BC2Slave] = 4
	(*clockRolesAlgoMapping[AlgoDualNicBCWithSlaves])[BC2Master] = 5
	(*clockRolesAlgoMapping[AlgoDualNicBCWithSlaves])[Slave2] = 6

	// TelcoGM
	(*clockRolesAlgoMapping[AlgoTelcoGM])[Grandmaster] = 0

	// ExtGM variants
	(*clockRolesAlgoMapping[AlgoOCExtGM])[Slave1] = 0

	(*clockRolesAlgoMapping[AlgoDualFollowerExtGM])[Slave1] = 0
	(*clockRolesAlgoMapping[AlgoDualFollowerExtGM])[Slave2] = 1

	(*clockRolesAlgoMapping[AlgoBCExtGM])[BC1Slave] = 0
	(*clockRolesAlgoMapping[AlgoBCExtGM])[BC1Master] = 1

	(*clockRolesAlgoMapping[AlgoBCWithSlavesExtGM])[BC1Slave] = 0
	(*clockRolesAlgoMapping[AlgoBCWithSlavesExtGM])[BC1Master] = 1
	(*clockRolesAlgoMapping[AlgoBCWithSlavesExtGM])[Slave1] = 2

	(*clockRolesAlgoMapping[AlgoDualNicBCExtGM])[BC1Slave] = 0
	(*clockRolesAlgoMapping[AlgoDualNicBCExtGM])[BC1Master] = 1
	(*clockRolesAlgoMapping[AlgoDualNicBCExtGM])[BC2Slave] = 2
	(*clockRolesAlgoMapping[AlgoDualNicBCExtGM])[BC2Master] = 3

	(*clockRolesAlgoMapping[AlgoDualNicBCWithSlavesExtGM])[BC1Slave] = 0
	(*clockRolesAlgoMapping[AlgoDualNicBCWithSlavesExtGM])[BC1Master] = 1
	(*clockRolesAlgoMapping[AlgoDualNicBCWithSlavesExtGM])[BC2Slave] = 2
	(*clockRolesAlgoMapping[AlgoDualNicBCWithSlavesExtGM])[BC2Master] = 3
	(*clockRolesAlgoMapping[AlgoDualNicBCWithSlavesExtGM])[Slave1] = 4
	(*clockRolesAlgoMapping[AlgoDualNicBCWithSlavesExtGM])[Slave2] = 5

	// Solve all problems, recovering from panics for unsolvable topologies
	for _, name := range enabledProblems {
		solver.GlobalConfig.InitProblem(name, *problems[name], *clockRolesAlgoMapping[name])
		func() {
			defer func() {
				if r := recover(); r != nil {
					logrus.Debugf("Solver panic for %s (topology not supported): %v", name, r)
				}
			}()
			solver.GlobalConfig.Run(name)
		}()
	}
	solver.GlobalConfig.PrintFirstSolution()
}
