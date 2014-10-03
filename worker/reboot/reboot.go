package reboot

import (
	"github.com/juju/errors"
	"github.com/juju/loggo"
	"github.com/juju/names"
	"github.com/juju/utils/fslock"
	"launchpad.net/tomb"

	"github.com/juju/juju/agent"
	"github.com/juju/juju/api/reboot"
	"github.com/juju/juju/api/watcher"
	"github.com/juju/juju/apiserver/params"
	"github.com/juju/juju/worker"
)

var logger = loggo.GetLogger("juju.worker.reboot")

const RebootMessage = "preparing for reboot"
const RebootLock = "machine-reboot-lock"

var _ worker.NotifyWatchHandler = (*Reboot)(nil)

// The reboot worker listens for changes to the reboot flag and
// exists with worker.ErrRebootMachine if the machine should reboot or
// with worker.ErrShutdownMachine if it should shutdown. This will be picked
// up by the machine agent as a fatal error and will do the
// right thing (reboot or shutdown)
type Reboot struct {
	tomb        tomb.Tomb
	st          *reboot.State
	tag         names.MachineTag
	machineLock *fslock.Lock
	rebootLock  *fslock.Lock
}

func NewReboot(st *reboot.State, agentConfig agent.Config, machineLock *fslock.Lock) (worker.Worker, error) {
	tag, ok := agentConfig.Tag().(names.MachineTag)
	if !ok {
		return nil, errors.Errorf("Expected names.MachineTag, got %T: %v", agentConfig.Tag(), agentConfig.Tag())
	}
	rebootLock, err := fslock.NewLock(agent.DefaultLockDir, RebootLock)
	if err != nil {
		return nil, err
	}
	r := &Reboot{
		st:          st,
		tag:         tag,
		machineLock: machineLock,
		rebootLock:  rebootLock,
	}
	return worker.NewNotifyWorker(r), nil
}

func (r *Reboot) breakHookLock() error {
	var err error
	if r.machineLock.Message() == RebootMessage {
		// Not a lock held by the machne agent in order to reboot
		err = r.machineLock.BreakLock()
		if err != nil {
			return err
		}
	}
	if r.rebootLock.IsLocked() {
		err = r.rebootLock.BreakLock()
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *Reboot) checkForRebootState() error {
	if r.rebootLock.IsLocked() == false {
		return nil
	}
	defer r.breakHookLock()

	// Clear reboot flag
	err := r.st.ClearReboot()
	if err != nil {
		logger.Errorf("Failed to clear reboot flag: %v", err)
		return err
	}
	return nil
}

func (r *Reboot) SetUp() (watcher.NotifyWatcher, error) {
	logger.Debugf("Reboot worker setup")
	err := r.checkForRebootState()
	if err != nil {
		return nil, errors.Trace(err)
	}
	watcher, err := r.st.WatchForRebootEvent()
	if err != nil {
		return nil, err
	}
	return watcher, nil
}

func (r *Reboot) Handle() error {
	rAction, err := r.st.GetRebootAction()
	if err != nil {
		return err
	}
	logger.Debugf("Reboot worker got action: %v", rAction)
	switch rAction {
	case params.ShouldReboot:
		r.machineLock.Lock(RebootMessage)
		r.rebootLock.Lock(RebootMessage)
		return worker.ErrRebootMachine
	case params.ShouldShutdown:
		r.machineLock.Lock(RebootMessage)
		r.rebootLock.Lock(RebootMessage)
		return worker.ErrShutdownMachine
	}
	return nil
}

func (r *Reboot) TearDown() error {
	// nothing to teardown.
	return nil
}
