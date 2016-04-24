package groupmember

import (
	"sync"
	"testing"
	"time"

	"github.com/mailgun/kafka-pixy/actor"
	"github.com/mailgun/kafka-pixy/config"
	"github.com/mailgun/kafka-pixy/none"
	"github.com/mailgun/kafka-pixy/testhelpers"
	"github.com/wvanbergen/kazoo-go"
	. "gopkg.in/check.v1"
)

func Test(t *testing.T) {
	TestingT(t)
}

type GroupRegistratorSuite struct {
	cid       *actor.ID
	kazooConn *kazoo.Kazoo
}

var _ = Suite(&GroupRegistratorSuite{})

func (s *GroupRegistratorSuite) SetUpSuite(c *C) {
	testhelpers.InitLogging(c)
	s.cid = actor.RootID.NewChild("gm-test")
	var err error
	s.kazooConn, err = kazoo.NewKazoo(testhelpers.ZookeeperPeers, kazoo.NewConfig())
	c.Assert(err, IsNil)
}

func (s *GroupRegistratorSuite) TestNormalizeTopics(c *C) {
	c.Assert(normalizeTopics(nil), DeepEquals, []string(nil))
	c.Assert(normalizeTopics([]string{}), DeepEquals, []string(nil))
	c.Assert(normalizeTopics([]string{"c", "a", "b"}), DeepEquals, []string{"a", "b", "c"})

	c.Assert(normalizeTopics([]string{"c", "a", "b"}), Not(DeepEquals), []string{"a", "b"})
}

func (s *GroupRegistratorSuite) TestTopicsEqual(c *C) {
	c.Assert(topicsEqual([]string{}, nil), Equals, true)
	c.Assert(topicsEqual(nil, []string{}), Equals, true)
	c.Assert(topicsEqual([]string{}, []string{}), Equals, true)
	c.Assert(topicsEqual([]string{"a"}, []string{"a"}), Equals, true)
	c.Assert(topicsEqual([]string{"a", "b", "c"}, []string{"a", "b", "c"}), Equals, true)

	c.Assert(topicsEqual([]string{"a", "b", "c"}, []string{"a", "b"}), Equals, false)
	c.Assert(topicsEqual([]string{"a", "b"}, []string{"b", "a"}), Equals, false)
}

func (s *GroupRegistratorSuite) TestSubscriptionsEqual(c *C) {
	c.Assert(subscriptionsEqual(nil, nil), Equals, true)
	c.Assert(subscriptionsEqual(map[string][]string{}, nil), Equals, true)
	c.Assert(subscriptionsEqual(nil, map[string][]string{}), Equals, true)
	c.Assert(subscriptionsEqual(map[string][]string{}, map[string][]string{}), Equals, true)

	c.Assert(subscriptionsEqual(
		map[string][]string{
			"m1": {"a", "b"},
			"m2": {"c", "d", "e"},
		},
		map[string][]string{
			"m1": {"a", "b"},
			"m2": {"c", "d", "e"},
		}), Equals, true)
}

// When a list of topics is sent to the `topics()` channel, a membership change
// is received with the same list of topics for the registrator name.
func (s *GroupRegistratorSuite) TestSimpleSubscribe(c *C) {
	// Given
	cfg := config.Default()
	cfg.Consumer.RebalanceDelay = 200 * time.Millisecond
	gm := Spawn("gm_test", "m1", cfg, s.kazooConn)
	defer gm.Stop()

	// When
	gm.Topics() <- []string{"foo", "bar"}

	// Then
	c.Assert(<-gm.Subscriptions(), DeepEquals,
		map[string][]string{"m1": {"bar", "foo"}})
}

// When topic subscription changes occur in close succession only one
// membership change notification is received back with the most recent topic
// list for the registrator name.
func (s *GroupRegistratorSuite) TestSubscribeSequence(c *C) {
	// Given
	cfg := config.Default()
	cfg.Consumer.RebalanceDelay = 200 * time.Millisecond
	gm := Spawn("gm_test", "m1", cfg, s.kazooConn)
	defer gm.Stop()
	gm.Topics() <- []string{"foo", "bar"}

	// When
	gm.Topics() <- []string{"blah", "bazz"}

	// Then
	c.Assert(<-gm.Subscriptions(), DeepEquals,
		map[string][]string{"m1": {"bazz", "blah"}})
}

// If a group member resubscribes to the same list of topics, then nothing is
// updated.
func (s *GroupRegistratorSuite) TestReSubscribe(c *C) {
	// Given
	cfg := config.Default()
	cfg.Consumer.RebalanceDelay = 100 * time.Millisecond

	gm1 := Spawn("gm_test", "m1", cfg, s.kazooConn)
	defer gm1.Stop()
	gm1.Topics() <- []string{"foo", "bar"}

	gm2 := Spawn("gm_test", "m2", cfg, s.kazooConn)
	defer gm2.Stop()
	gm2.Topics() <- []string{"bazz", "bar"}

	membership := map[string][]string{
		"m1": {"bar", "foo"},
		"m2": {"bar", "bazz"},
	}
	c.Assert(<-gm1.Subscriptions(), DeepEquals, membership)
	c.Assert(<-gm2.Subscriptions(), DeepEquals, membership)

	// When
	gm1.Topics() <- []string{"foo", "bar"}

	// Then
	select {
	case update := <-gm1.Subscriptions():
		c.Errorf("Unexpected update: %v", update)
	case update := <-gm2.Subscriptions():
		c.Errorf("Unexpected update: %v", update)
	case <-time.After(300 * time.Millisecond):
	}
}

// To unsubscribe from all topics an empty topic list can be sent.
func (s *GroupRegistratorSuite) TestSubscribeToNothing(c *C) {
	// Given
	cfg := config.Default()
	cfg.Consumer.RebalanceDelay = 100 * time.Millisecond
	gm1 := Spawn("gm_test", "m1", cfg, s.kazooConn)
	defer gm1.Stop()
	gm2 := Spawn("gm_test", "m2", cfg, s.kazooConn)
	defer gm2.Stop()
	gm1.Topics() <- []string{"foo", "bar"}
	gm2.Topics() <- []string{"foo"}
	c.Assert(<-gm1.Subscriptions(), DeepEquals,
		map[string][]string{"m1": {"bar", "foo"}, "m2": {"foo"}})
	c.Assert(<-gm2.Subscriptions(), DeepEquals,
		map[string][]string{"m1": {"bar", "foo"}, "m2": {"foo"}})

	// When
	gm1.Topics() <- []string{}

	// Then
	c.Assert(<-gm2.Subscriptions(), DeepEquals,
		map[string][]string{"m2": {"foo"}})
	select {
	case update := <-gm1.Subscriptions():
		c.Errorf("Unexpected update: %v", update)
	case <-time.After(300 * time.Millisecond):
	}
}

// To unsubscribe from all topics nil value can be sent.
func (s *GroupRegistratorSuite) TestSubscribeToNil(c *C) {
	// Given
	cfg := config.Default()
	cfg.Consumer.RebalanceDelay = 100 * time.Millisecond
	gm1 := Spawn("gm_test", "m1", cfg, s.kazooConn)
	defer gm1.Stop()
	gm2 := Spawn("gm_test", "m2", cfg, s.kazooConn)
	defer gm2.Stop()
	gm1.Topics() <- []string{"foo", "bar"}
	gm2.Topics() <- []string{"foo"}
	c.Assert(<-gm1.Subscriptions(), DeepEquals,
		map[string][]string{"m1": {"bar", "foo"}, "m2": {"foo"}})
	c.Assert(<-gm2.Subscriptions(), DeepEquals,
		map[string][]string{"m1": {"bar", "foo"}, "m2": {"foo"}})

	// When
	gm1.Topics() <- nil

	// Then
	c.Assert(<-gm2.Subscriptions(), DeepEquals,
		map[string][]string{"m2": {"foo"}})
	select {
	case update := <-gm1.Subscriptions():
		c.Errorf("Unexpected update: %v", update)
	case <-time.After(300 * time.Millisecond):
	}
}

// When several different registrator instances subscribe to the same group,
// they all receive identical membership change notifications that include all
// their subscription.
func (s *GroupRegistratorSuite) TestMembershipChanges(c *C) {
	// Given
	cfg := config.Default()
	cfg.Consumer.RebalanceDelay = 200 * time.Millisecond
	gm1 := Spawn("gm_test", "m1", cfg, s.kazooConn)
	defer gm1.Stop()
	gm2 := Spawn("gm_test", "m2", cfg, s.kazooConn)
	defer gm2.Stop()
	gm3 := Spawn("gm_test", "m3", cfg, s.kazooConn)
	defer gm3.Stop()

	// When
	gm1.Topics() <- []string{"foo", "bar"}
	gm2.Topics() <- []string{"foo"}
	gm3.Topics() <- []string{"foo", "bazz", "blah"}

	// Then
	membership := map[string][]string{
		"m1": {"bar", "foo"},
		"m2": {"foo"},
		"m3": {"bazz", "blah", "foo"}}

	c.Assert(<-gm1.Subscriptions(), DeepEquals, membership)
	c.Assert(<-gm2.Subscriptions(), DeepEquals, membership)
	c.Assert(<-gm3.Subscriptions(), DeepEquals, membership)
}

// When one of the group members generates a rapid sequence of subscription
// changes so that at the end its subscription is the same as in the beginning
// of the sequence then other members won't be notified of such changes.
func (s *GroupRegistratorSuite) TestRedundantUpdateIgnored(c *C) {
	// Given
	cfg := config.Default()
	cfg.Consumer.RebalanceDelay = 200 * time.Millisecond
	gm1 := Spawn("gm_test", "m1", cfg, s.kazooConn)
	defer gm1.Stop()
	gm2 := Spawn("gm_test", "m2", cfg, s.kazooConn)
	defer gm2.Stop()

	gm1.Topics() <- []string{"foo", "bar"}
	gm2.Topics() <- []string{"foo", "bazz", "blah"}

	c.Assert(<-gm1.Subscriptions(), DeepEquals,
		map[string][]string{
			"m1": {"bar", "foo"},
			"m2": {"bazz", "blah", "foo"}})

	// When
	gm2.Topics() <- []string{"bar"}
	gm2.Topics() <- []string{"foo", "bazz", "blah"}

	// Then
	select {
	case update := <-gm1.Subscriptions():
		c.Errorf("Unexpected update: %v", update)
	case <-time.After(300 * time.Millisecond):
	}
}

// When a group registrator claims a topic partitions it becomes its owner.
func (s *GroupRegistratorSuite) TestClaimPartition(c *C) {
	// Given
	cfg := config.Default()
	gm := Spawn("gm_test", "m1", cfg, s.kazooConn)
	defer gm.Stop()
	cancelCh := make(chan none.T)

	owner, err := partitionOwner(gm, "foo", 1)
	c.Assert(err, IsNil)
	c.Assert(owner, Equals, "")

	// When
	claim1 := gm.ClaimPartition(s.cid, "foo", 1, cancelCh)
	defer claim1()

	// Then
	owner, err = partitionOwner(gm, "foo", 1)
	c.Assert(err, IsNil)
	c.Assert(owner, Equals, "m1")
}

// If a consumer group member instance tries to acquire a partition that has
// already been acquired by another member then it fails.
func (s *GroupRegistratorSuite) TestClaimPartitionClaimed(c *C) {
	// Given
	cfg := config.Default()
	gm1 := Spawn("gm_test", "m1", cfg, s.kazooConn)
	defer gm1.Stop()
	gm2 := Spawn("gm_test", "m2", cfg, s.kazooConn)
	defer gm2.Stop()
	cancelCh := make(chan none.T)
	close(cancelCh) // there will be no retries

	claim1 := gm1.ClaimPartition(s.cid, "foo", 1, cancelCh)
	defer claim1()

	// When
	claim2 := gm2.ClaimPartition(s.cid, "foo", 1, cancelCh)
	defer claim2()

	// Then
	owner, err := partitionOwner(gm1, "foo", 1)
	c.Assert(err, IsNil)
	c.Assert(owner, Equals, "m1")
}

// It is ok to claim the same partition twice by the same group member.
func (s *GroupRegistratorSuite) TestClaimPartitionTwice(c *C) {
	// Given
	cfg := config.Default()
	gm := Spawn("gm_test", "m1", cfg, s.kazooConn)
	defer gm.Stop()
	cancelCh := make(chan none.T)

	// When
	claim1 := gm.ClaimPartition(s.cid, "foo", 1, cancelCh)
	defer claim1()
	claim2 := gm.ClaimPartition(s.cid, "foo", 1, cancelCh)
	defer claim2()

	// Then
	owner, err := partitionOwner(gm, "foo", 1)
	c.Assert(err, IsNil)
	c.Assert(owner, Equals, "m1")
}

// If a partition has been claimed more then once then it is release as soon as
// any of the claims is revoked.
func (s *GroupRegistratorSuite) TestReleasePartition(c *C) {
	// Given
	cfg := config.Default()
	gm := Spawn("gm_test", "m1", cfg, s.kazooConn)
	defer gm.Stop()
	cancelCh := make(chan none.T)
	claim1 := gm.ClaimPartition(s.cid, "foo", 1, cancelCh)
	claim2 := gm.ClaimPartition(s.cid, "foo", 1, cancelCh)

	// When
	claim2() // the second claim is revoked here but it could have been any.

	// Then
	owner, err := partitionOwner(gm, "foo", 1)
	c.Assert(err, IsNil)
	c.Assert(owner, Equals, "")

	claim1()
}

// If a partition is claimed by another group member then `ClaimPartition` call
// blocks until it is released.
func (s *GroupRegistratorSuite) TestClaimPartitionParallel(c *C) {
	// Given
	cfg := config.Default()
	gm1 := Spawn("gm_test", "m1", cfg, s.kazooConn)
	defer gm1.Stop()
	gm2 := Spawn("gm_test", "m2", cfg, s.kazooConn)
	defer gm2.Stop()
	cancelCh := make(chan none.T)

	claim1 := gm1.ClaimPartition(s.cid, "foo", 1, cancelCh)
	go func() {
		time.Sleep(300 * time.Millisecond)
		claim1()
	}()

	// When: block here until m1 releases the claim over foo/1.
	claim2 := gm2.ClaimPartition(s.cid, "foo", 1, cancelCh)
	defer claim2()

	// Then: the partition is claimed by m2.
	owner, err := partitionOwner(gm2, "foo", 1)
	c.Assert(err, IsNil)
	c.Assert(owner, Equals, "m2")
}

// If a partition is claimed by another group member then `ClaimPartition` call
// blocks until it is released.
func (s *GroupRegistratorSuite) TestClaimPartitionCanceled(c *C) {
	// Given
	cfg := config.Default()
	gm1 := Spawn("gm_test", "m1", cfg, s.kazooConn)
	defer gm1.Stop()
	gm2 := Spawn("gm_test", "m2", cfg, s.kazooConn)
	defer gm2.Stop()
	cancelCh1 := make(chan none.T)
	cancelCh2 := make(chan none.T)
	wg := &sync.WaitGroup{}

	claim1 := gm1.ClaimPartition(s.cid, "foo", 1, cancelCh1)
	wg.Add(1)
	go func() {
		wg.Done()
		time.Sleep(300 * time.Millisecond)
		claim1()
	}()

	// This goroutine will cancel the claim of m2 before, m1 releases the partition.
	wg.Add(1)
	go func() {
		wg.Done()
		time.Sleep(150 * time.Millisecond)
		close(cancelCh2)
	}()

	// When
	claim2 := gm2.ClaimPartition(s.cid, "foo", 1, cancelCh2)
	defer claim2()

	// Then: the partition is still claimed by m1.
	owner, err := partitionOwner(gm2, "foo", 1)
	c.Assert(err, IsNil)
	c.Assert(owner, Equals, "m1")

	// Wait for all test goroutines to stop.
	wg.Wait()
}

// partitionOwner returns the id of the consumer group member that has claimed
// the specified topic/partition.
func partitionOwner(gm *T, topic string, partition int32) (string, error) {
	owner, err := gm.groupZNode.PartitionOwner(topic, partition)
	if err != nil {
		return "", err
	}
	if owner == nil {
		return "", nil
	}
	return owner.ID, nil
}
