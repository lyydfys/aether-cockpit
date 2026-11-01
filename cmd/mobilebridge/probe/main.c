#include <arpa/inet.h>
#include <dlfcn.h>
#include <errno.h>
#include <netinet/in.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/time.h>
#include <unistd.h>

typedef int (*version_fn)(char *, int);
typedef int (*start_fn)(const char *, char *, int);
typedef int (*status_fn)(char *, int);
typedef int (*stop_fn)(char *, int);

static const char *CONFIG =
    "{\"dataDir\":\"/data/local/tmp/aether-mobilebridge/data\","
    "\"listenHost\":\"127.0.0.1\",\"port\":0,\"platform\":\"android\"}";

static void load_symbol(void *handle, const char *name, void *target, size_t target_size) {
    void *symbol = dlsym(handle, name);
    if (symbol == NULL) {
        fprintf(stderr, "missing symbol %s: %s\n", name, dlerror());
        exit(3);
    }
    if (target_size > sizeof(symbol)) {
        target_size = sizeof(symbol);
    }
    memcpy(target, &symbol, target_size);
}

static int extract_string(const char *json, const char *key, char *dst, size_t cap) {
    char pattern[64];
    int written = snprintf(pattern, sizeof(pattern), "\"%s\":\"", key);
    if (written < 0 || (size_t)written >= sizeof(pattern)) {
        return 0;
    }
    const char *start = strstr(json, pattern);
    if (start == NULL) {
        return 0;
    }
    start += (size_t)written;
    const char *end = strchr(start, '"');
    if (end == NULL) {
        return 0;
    }
    size_t length = (size_t)(end - start);
    if (length >= cap) {
        length = cap - 1;
    }
    memcpy(dst, start, length);
    dst[length] = '\0';
    return 1;
}

static int parse_port(const char *addr) {
    const char *colon = strrchr(addr, ':');
    if (colon == NULL) {
        return -1;
    }
    char *end = NULL;
    long port = strtol(colon + 1, &end, 10);
    if (end == colon + 1 || *end != '\0' || port <= 0 || port > 65535) {
        return -1;
    }
    return (int)port;
}

static void set_timeout(int fd) {
    struct timeval timeout = {.tv_sec = 3, .tv_usec = 0};
    setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &timeout, sizeof(timeout));
    setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &timeout, sizeof(timeout));
}

static int http_health(int port, char *response, size_t cap) {
    if (cap > 0) {
        response[0] = '\0';
    }
    int fd = socket(AF_INET, SOCK_STREAM, 0);
    if (fd < 0) {
        return 0;
    }
    set_timeout(fd);

    struct sockaddr_in sin;
    memset(&sin, 0, sizeof(sin));
    sin.sin_family = AF_INET;
    sin.sin_port = htons((uint16_t)port);
    if (inet_pton(AF_INET, "127.0.0.1", &sin.sin_addr) != 1) {
        close(fd);
        return 0;
    }
    if (connect(fd, (struct sockaddr *)&sin, sizeof(sin)) != 0) {
        close(fd);
        return 0;
    }

    const char *request =
        "GET /healthz HTTP/1.1\r\n"
        "Host: 127.0.0.1\r\n"
        "Connection: close\r\n"
        "\r\n";
    size_t request_length = strlen(request);
    size_t sent = 0;
    while (sent < request_length) {
        ssize_t n = send(fd, request + sent, request_length - sent, 0);
        if (n <= 0) {
            close(fd);
            return 0;
        }
        sent += (size_t)n;
    }

    size_t total = 0;
    while (total + 1 < cap) {
        ssize_t n = recv(fd, response + total, cap - 1 - total, 0);
        if (n <= 0) {
            break;
        }
        total += (size_t)n;
    }
    response[total] = '\0';
    close(fd);
    return total > 0;
}

static int port_is_free(int port) {
    for (int attempt = 0; attempt < 5; attempt++) {
        int fd = socket(AF_INET, SOCK_STREAM, 0);
        if (fd < 0) {
            return 0;
        }
        int one = 1;
        setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &one, sizeof(one));
        struct sockaddr_in sin;
        memset(&sin, 0, sizeof(sin));
        sin.sin_family = AF_INET;
        sin.sin_port = htons((uint16_t)port);
        sin.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
        int bound = bind(fd, (struct sockaddr *)&sin, sizeof(sin));
        close(fd);
        if (bound == 0) {
            return 1;
        }
        usleep(100000);
    }
    return 0;
}

int main(int argc, char **argv) {
    setvbuf(stdout, NULL, _IOLBF, 0);

    const char *library = "./libaether_mobilebridge.so";
    int cycles = 3;
    for (int i = 1; i < argc; i++) {
        if (strcmp(argv[i], "--cycles") == 0 && i + 1 < argc) {
            cycles = atoi(argv[++i]);
        } else {
            library = argv[i];
        }
    }
    if (cycles < 1 || cycles > 10) {
        fprintf(stderr, "cycles must be between 1 and 10\n");
        return 2;
    }

    void *handle = dlopen(library, RTLD_NOW | RTLD_LOCAL);
    if (handle == NULL) {
        fprintf(stderr, "dlopen %s failed: %s\n", library, dlerror());
        return 2;
    }

    version_fn version;
    start_fn start;
    status_fn status;
    stop_fn stop;
    load_symbol(handle, "nativeBridgeVersion", &version, sizeof(version));
    load_symbol(handle, "nativeStart", &start, sizeof(start));
    load_symbol(handle, "nativeGetStatus", &status, sizeof(status));
    load_symbol(handle, "nativeStop", &stop, sizeof(stop));

    char version_text[64];
    int n = version(version_text, (int)sizeof(version_text));
    if (n <= 0 || strcmp(version_text, "mobilebridge/2") != 0) {
        fprintf(stderr, "unexpected ABI version: %s\n", n > 0 ? version_text : "<buffer too small>");
        return 4;
    }

    for (int cycle = 1; cycle <= cycles; cycle++) {
        char out[2048];
        n = start(CONFIG, out, (int)sizeof(out));
        if (n <= 0 || strstr(out, "\"state\":\"running\"") == NULL) {
            fprintf(stderr, "cycle=%d start failed: %s\n", cycle, n > 0 ? out : "<buffer too small>");
            return 5;
        }

        char addr[64];
        if (!extract_string(out, "addr", addr, sizeof(addr))) {
            fprintf(stderr, "cycle=%d missing addr: %s\n", cycle, out);
            return 5;
        }
        int port = parse_port(addr);
        if (port <= 0) {
            fprintf(stderr, "cycle=%d invalid addr: %s\n", cycle, addr);
            return 5;
        }

        n = status(out, (int)sizeof(out));
        if (n <= 0 || strstr(out, "\"state\":\"running\"") == NULL || strstr(out, addr) == NULL) {
            fprintf(stderr, "cycle=%d status mismatch: %s\n", cycle, n > 0 ? out : "<buffer too small>");
            return 6;
        }

        char response[4096];
        if (!http_health(port, response, sizeof(response)) ||
            strstr(response, " 200 ") == NULL ||
            strstr(response, "\"status\":\"ok\"") == NULL) {
            fprintf(stderr, "cycle=%d health failed: %s\n", cycle, response);
            return 7;
        }

        n = stop(out, (int)sizeof(out));
        if (n <= 0 || strstr(out, "\"state\":\"stopped\"") == NULL) {
            fprintf(stderr, "cycle=%d stop failed: %s\n", cycle, n > 0 ? out : "<buffer too small>");
            return 8;
        }

        n = status(out, (int)sizeof(out));
        if (n <= 0 || strstr(out, "\"state\":\"stopped\"") == NULL) {
            fprintf(stderr, "cycle=%d post-stop status failed: %s\n", cycle, n > 0 ? out : "<buffer too small>");
            return 9;
        }

        usleep(100000);
        bool released = port_is_free(port);
        printf("cycle=%d addr=%s health=ok stop=ok port-released=%s\n",
               cycle, addr, released ? "true" : "false");
        if (!released) {
            return 10;
        }
    }

    printf("probe=ok cycles=%d abi=%s\n", cycles, version_text);
    return 0;
}
