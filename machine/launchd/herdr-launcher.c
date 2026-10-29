// The process macOS holds responsible for the herdr server's privacy grants.
//
// A privacy grant is pinned to the exact contents of an ad-hoc signed binary, and
// herdr's release binary is ad-hoc signed, so every `herdr update` used to drop
// Screen Recording, Full Disk Access and the rest, and Tim had to grant them again
// on the Mac's own screen. macOS asks TCC's questions of the responsible process
// rather than of the one that called: launchd makes a job's own process
// responsible for itself, and a child inherits its parent's responsibility unless
// it disclaims. So the LaunchAgent starts this program, this program runs herdr as
// its child, and the grants belong to these bytes, which change only when this file
// does.
//
// Which is also why it stays the parent and never execs herdr: exec would put
// herdr's own contents back in front of TCC. Being a parent it would not otherwise
// need to be, it has to behave as if it were not there — forward the signals
// launchd and a restart by hand send, and exit the way herdr exited, so KeepAlive
// and `launchctl bootout` see what they saw when the agent ran herdr directly.
//
// C rather than Go and built once rather than by the build-in-place wrapper the
// tools under hachiko/ use, for the same single reason: identity. The wrapper
// rebuilds whenever its sources move and against whatever go mise has, and a
// rebuild is new bytes and so a dropped grant. install.sh compiles this against the
// hash of this file and leaves it alone otherwise.

#include <errno.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/wait.h>
#include <unistd.h>

// The child's pid, read by a signal handler, so sig_atomic_t and nothing larger.
static volatile sig_atomic_t child = 0;

static void forward(int sig) {
  if (child > 0) {
    kill((pid_t)child, sig);
  }
}

int main(int argc, char *argv[]) {
  const char *home = getenv("HOME");

  // launchd hands a gui-domain agent the account's HOME, and this is the only
  // thing here that needs to know where herdr is. An absolute path from the
  // caller is what a plist may not hold and what would let anything on the Mac
  // borrow these grants for a program of its choosing.
  if (home == NULL || home[0] != '/') {
    fprintf(stderr, "herdr-launcher: HOME is not an absolute path\n");
    return 78; // EX_CONFIG, what launchd calls a job it cannot run as written
  }

  char herdr[4096];
  if ((size_t)snprintf(herdr, sizeof herdr, "%s/.local/bin/herdr", home) >= sizeof herdr) {
    fprintf(stderr, "herdr-launcher: HOME is too long a path\n");
    return 78;
  }

  char **args = calloc((size_t)argc + 1, sizeof *args);
  if (args == NULL) {
    fprintf(stderr, "herdr-launcher: out of memory\n");
    return 71;
  }
  args[0] = herdr;
  for (int i = 1; i < argc; i++) {
    args[i] = argv[i];
  }

  // Blocked across the fork so a SIGTERM arriving between it and the assignment
  // below is delivered to a handler that already knows the pid, rather than to one
  // that silently drops it and leaves herdr running under a launcher that is gone.
  int signals[] = {SIGTERM, SIGINT, SIGHUP};
  sigset_t forwarded, previous;
  sigemptyset(&forwarded);
  for (size_t i = 0; i < sizeof signals / sizeof *signals; i++) {
    sigaddset(&forwarded, signals[i]);
  }
  if (sigprocmask(SIG_BLOCK, &forwarded, &previous) != 0) {
    fprintf(stderr, "herdr-launcher: cannot block signals: %s\n", strerror(errno));
    return 71;
  }

  struct sigaction forwarding;
  memset(&forwarding, 0, sizeof forwarding);
  forwarding.sa_handler = forward;
  sigemptyset(&forwarding.sa_mask);
  for (size_t i = 0; i < sizeof signals / sizeof *signals; i++) {
    if (sigaction(signals[i], &forwarding, NULL) != 0) {
      fprintf(stderr, "herdr-launcher: cannot handle signal %d: %s\n", signals[i], strerror(errno));
      return 71;
    }
  }

  pid_t pid = fork();
  if (pid < 0) {
    fprintf(stderr, "herdr-launcher: cannot fork: %s\n", strerror(errno));
    return 71;
  }

  if (pid == 0) {
    // execv restores a handled signal to its default, but not a blocked one: herdr
    // would start deaf to the three it is sent. No process group of its own either,
    // so launchd's own cleanup still reaches herdr if this process dies first.
    sigprocmask(SIG_SETMASK, &previous, NULL);
    execv(herdr, args);
    fprintf(stderr, "herdr-launcher: cannot run %s: %s\n", herdr, strerror(errno));
    _exit(127);
  }

  child = pid;
  sigprocmask(SIG_SETMASK, &previous, NULL);

  int status = 0;
  while (waitpid(pid, &status, 0) < 0) {
    if (errno != EINTR) {
      fprintf(stderr, "herdr-launcher: cannot wait for herdr: %s\n", strerror(errno));
      return 71;
    }
  }

  if (WIFSIGNALED(status)) {
    // Dying of the same signal rather than exiting 128+n, so that what launchd
    // records of the job, and what a shell waiting on it sees, is what it saw when
    // the agent ran herdr itself.
    int sig = WTERMSIG(status);
    signal(sig, SIG_DFL);
    sigemptyset(&forwarded);
    sigaddset(&forwarded, sig);
    sigprocmask(SIG_UNBLOCK, &forwarded, NULL);
    raise(sig);
  }

  return WEXITSTATUS(status);
}
