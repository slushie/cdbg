/* basic application that writes state to a file forever */

#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>
#include <signal.h>

static int caught_signal = 0;

void interrupt_handler(int sig) {
    caught_signal = sig;
}

int main(int argc, char* argv[]) {
    FILE* fp;
    if (argc != 2) {
        fprintf(stderr, "usage: %s <file>\n", argv[0]);
        return EXIT_FAILURE;
    }

    signal(SIGINT, interrupt_handler);
    signal(SIGHUP, interrupt_handler);
    signal(SIGTERM, interrupt_handler);

    int state = 0;
    while (!caught_signal) {
        fp = fopen(argv[1], "w");
        fprintf(stdout, "%d\n", state);
        fprintf(fp, "%d\n", state ++);
        fflush(fp);
        fclose(fp);
        sleep(1);
    }
    fprintf(stderr, "killed by signal %d\n", caught_signal);
}
