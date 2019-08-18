/* basic application that writes state to a file forever */

#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>

int main(int argc, char* argv[]) {
    FILE* fp;
    if (argc != 2) {
        fprintf(stderr, "usage: %s <file>\n", argv[0]);
        return EXIT_FAILURE;
    }

    int state = 0;
    while (1) {
        fp = fopen(argv[1], "w");
        fprintf(stdout, "%d\n", state);
        fprintf(fp, "%d\n", state ++);
        fflush(fp);
        fclose(fp);
        sleep(1);
    }
}
