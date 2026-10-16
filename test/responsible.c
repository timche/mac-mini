// Prints the process macOS holds responsible for this one, which is what TCC asks
// its privacy questions of and pins a grant to. Built and run by assert.sh, to
// prove that a launchd job's children answer for the launcher rather than for
// themselves.
//
// responsibility_get_pid_responsible_for_pid is not in a header, so it is declared
// here. There is no public API for this and no command that reports it.

#include <libproc.h>
#include <stdio.h>
#include <unistd.h>

extern pid_t responsibility_get_pid_responsible_for_pid(pid_t);

int main(void) {
  pid_t responsible = responsibility_get_pid_responsible_for_pid(getpid());
  char path[4096] = "";

  if (responsible > 0) {
    proc_pidpath(responsible, path, sizeof path);
  }

  printf("responsible_pid=%d responsible_path=%s\n", responsible, path);
  return 0;
}
