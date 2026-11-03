// The process macOS holds responsible for the herdr-core core, and through it for
// the holder and every terminal underneath.
//
// macOS asks TCC's questions of the responsible process rather than of the one that
// called: launchd makes a job's own process responsible for itself, and a child
// inherits its parent's responsibility unless it disclaims. herdr-core is Developer
// ID signed, but as a bare binary with no bundle ID it cannot be given Local Network
// at all, which Bun and Node in a session need for *.orb.local, and a core a remote
// client starts over SSH answers for the SSH server instead. So the LaunchAgent
// starts this program, this program runs the core as its child, and the grants
// belong to these bytes, which change only when this file does. A grant to an
// ad-hoc signed binary is pinned to its exact contents, and a rebuild is a grant
// lost, which only Tim can give back, by hand on the Mac's own screen.
//
// It supervises rather than starting one core and waiting on it. TCC fixes a
// process's responsibility when the process is spawned, so the holder and the
// terminals under it answer for whoever started them for as long as they live, and
// a core that was itself the responsible process takes that with it when
// `hc server restart` replaces it — every terminal started afterwards answers for
// itself and has none of the grants. A process that never exits and is the parent
// of every core is what holds across a restart, so this one starts another core
// whenever the one it has exits and nobody asked it to stop.
//
// HC_LAUNCHD_DIRECT is what keeps the core a child at all. Outside an SSH session
// `herdr-core serve` otherwise re-launches itself through a launchd job of its own,
// which puts the bare herdr-core binary in front of TCC, where it cannot be given
// Local Network (herdrdev/herdr-core#83). Set to the pid the core is to stay under,
// it skips that.
//
// Which is also why this stays the parent and never execs the core: an exec would
// put herdr-core's own contents back in front of TCC. Being a parent nothing else
// needs, it behaves as though it were not there — forwards the signals launchd and
// a stop by hand send, and exits the way the core exited, so `launchctl bootout`
// sees what it saw when the agent ran the core directly.
//
// C rather than Go and built once rather than by the build-in-place wrapper the
// tools under tools/ use, for the same single reason: identity. The wrapper
// rebuilds whenever its sources move and against whatever go mise has, and a
// rebuild is new bytes and so a dropped grant. install.sh compiles this against the
// hash of this file and leaves it alone otherwise.

#include <errno.h>
#include <signal.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>

// A core that has run this long is one that was working, so the next failure starts
// from the shortest wait again.
#define SETTLED_SECONDS 60
// Shorter than this is a core that never came up, the shape a moved subcommand
// takes.
#define STILLBORN_SECONDS 2
#define BACKOFF_CEILING 30

// Read by a signal handler, so sig_atomic_t and nothing larger.
static volatile sig_atomic_t child = 0;
static volatile sig_atomic_t stop_signal = 0;

static void forward(int sig) {
  stop_signal = sig;
  if (child > 0) {
    kill((pid_t)child, sig);
  }
}

// Timestamped, because this goes to a log appended to for the life of the Mac and a
// bare "started the core" there says nothing about when it happened.
static void say(const char *format, ...) {
  char when[32];
  time_t now = time(NULL);
  struct tm parts;

  if (localtime_r(&now, &parts) != NULL &&
      strftime(when, sizeof when, "%Y-%m-%d %H:%M:%S", &parts) > 0) {
    fprintf(stderr, "%s ", when);
  }
  fprintf(stderr, "herdr-core-launcher: ");

  va_list args;
  va_start(args, format);
  vfprintf(stderr, format, args);
  va_end(args);

  fputc('\n', stderr);
  fflush(stderr);
}

int main(int argc, char *argv[]) {
  (void)argv;

  // Nothing is passed through: the one thing this may ever run is `herdr-core
  // serve`, and an argument from the caller would be an argument to the core under
  // this process's grants.
  if (argc > 1) {
    say("takes no arguments");
    return 64; // EX_USAGE
  }

  // launchd hands a gui-domain agent the account's HOME, and this is the only thing
  // here that needs to know where herdr-core is. An absolute path from the caller is
  // what a plist may not hold and what would let anything on the Mac borrow these
  // grants for a program of its choosing.
  const char *home = getenv("HOME");
  if (home == NULL || home[0] != '/') {
    say("HOME is not an absolute path");
    return 78; // EX_CONFIG, what launchd calls a job it cannot run as written
  }

  char core[4096];
  if ((size_t)snprintf(core, sizeof core, "%s/.local/bin/herdr-core", home) >= sizeof core) {
    say("HOME is too long a path");
    return 78;
  }

  // Blocked around each fork so a SIGTERM arriving between it and the assignment
  // below is delivered to a handler that already knows the pid, rather than to one
  // that silently drops it and leaves a core running under a launcher that is gone.
  // Blocked again from each wait until the next fork, so a stop asked for between
  // two cores is seen before another one is started.
  int signals[] = {SIGTERM, SIGINT, SIGHUP};
  sigset_t forwarded, previous;
  sigemptyset(&forwarded);
  for (size_t i = 0; i < sizeof signals / sizeof *signals; i++) {
    sigaddset(&forwarded, signals[i]);
  }
  if (sigprocmask(SIG_BLOCK, &forwarded, &previous) != 0) {
    say("cannot block signals: %s", strerror(errno));
    return 71;
  }

  struct sigaction forwarding;
  memset(&forwarding, 0, sizeof forwarding);
  forwarding.sa_handler = forward;
  sigemptyset(&forwarding.sa_mask);
  for (size_t i = 0; i < sizeof signals / sizeof *signals; i++) {
    if (sigaction(signals[i], &forwarding, NULL) != 0) {
      say("cannot handle signal %d: %s", signals[i], strerror(errno));
      return 71;
    }
  }

  int status = 0;
  unsigned backoff = 1;
  int stillborn = 0;

  for (;;) {
    pid_t pid = fork();
    if (pid < 0) {
      say("cannot fork: %s", strerror(errno));
      return 71;
    }

    if (pid == 0) {
      char direct[32];
      snprintf(direct, sizeof direct, "%d", (int)getppid());
      setenv("HC_LAUNCHD_DIRECT", direct, 1);

      // exec restores a handled signal to its default, but not a blocked one: the
      // core would start deaf to the three it is sent. No process group of its own
      // either, so launchd's own cleanup still reaches the core if this process dies
      // first.
      sigprocmask(SIG_SETMASK, &previous, NULL);
      execl(core, core, "serve", (char *)NULL);
      say("cannot run %s: %s", core, strerror(errno));
      _exit(127);
    }

    child = pid;

    struct timespec started;
    clock_gettime(CLOCK_MONOTONIC, &started);
    say("started %s serve as pid %d", core, (int)pid);
    sigprocmask(SIG_SETMASK, &previous, NULL);

    while (waitpid(pid, &status, 0) < 0) {
      if (errno != EINTR) {
        say("cannot wait for the core: %s", strerror(errno));
        return 71;
      }
    }

    sigprocmask(SIG_BLOCK, &forwarded, NULL);
    child = 0;

    struct timespec ended;
    clock_gettime(CLOCK_MONOTONIC, &ended);
    long lived = (long)(ended.tv_sec - started.tv_sec);

    if (WIFSIGNALED(status)) {
      say("the core died of signal %d after %lds", WTERMSIG(status), lived);
    } else {
      say("the core exited %d after %lds", WEXITSTATUS(status), lived);
    }

    if (stop_signal != 0) {
      break;
    }

    // At once, with nothing in between, when the core was one that had come up: an
    // `hc server restart` starts a core of its own as soon as the old one is down, and
    // the one that takes the lock is the one every terminal from then on answers for.
    // A wait here, however short, is that race lost and the grants with it.
    if (lived >= STILLBORN_SECONDS) {
      stillborn = 0;
      if (lived >= SETTLED_SECONDS) {
        backoff = 1;
      }
      continue;
    }

    stillborn++;
    // Said at the second one and not again until a core comes up. A core that will not
    // stay up at all is one of these two things before it is anything else, and a line
    // every restart for as long as it loops would bury the restarts themselves.
    if (stillborn == 2) {
      say("the core has not stayed up for %ds twice over — either another core holds"
          " the lock, or `herdr-core serve` has changed under us; it is an internal"
          " subcommand, so check `herdr-core serve --help`",
          STILLBORN_SECONDS);
    }

    say("starting the core again in %us", backoff);

    // Unblocked across the wait, so a stop asked for during it is acted on rather
    // than sat out, and the remainder is what the interrupted sleep gives back.
    sigprocmask(SIG_SETMASK, &previous, NULL);
    struct timespec waiting = {(time_t)backoff, 0}, left;
    while (nanosleep(&waiting, &left) != 0 && errno == EINTR && stop_signal == 0) {
      waiting = left;
    }
    sigprocmask(SIG_BLOCK, &forwarded, NULL);

    if (stop_signal != 0) {
      break;
    }

    backoff = backoff * 2 > BACKOFF_CEILING ? BACKOFF_CEILING : backoff * 2;
  }

  if (WIFSIGNALED(status)) {
    // Dying of the same signal rather than exiting 128+n, so that what launchd
    // records of the job, and what a shell waiting on it sees, is what it saw when
    // the agent ran the core directly.
    int sig = WTERMSIG(status);
    signal(sig, SIG_DFL);
    sigemptyset(&forwarded);
    sigaddset(&forwarded, sig);
    sigprocmask(SIG_UNBLOCK, &forwarded, NULL);
    raise(sig);
  }

  return WEXITSTATUS(status);
}
