#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <linux/audit.h>
#include <linux/filter.h>
#include <linux/landlock.h>
#include <linux/magic.h>
#include <linux/seccomp.h>
#include <sched.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <sys/prctl.h>
#include <sys/resource.h>
#include <sys/stat.h>
#include <sys/vfs.h>
#include <sys/syscall.h>
#include <unistd.h>

#if !defined(__x86_64__) || defined(__ILP32__)
#error "The worker sandbox syscall policy requires Linux amd64"
#endif

#define ALLOW(name) \
    BPF_JUMP(BPF_JMP + BPF_JEQ + BPF_K, SYS_##name, 0, 1), \
    BPF_STMT(BPF_RET + BPF_K, SECCOMP_RET_ALLOW)
#define DENY SECCOMP_RET_ERRNO | ENOSYS

static int restrict_syscalls(void)
{
    struct sock_filter filter[] = {
        BPF_STMT(BPF_LD + BPF_W + BPF_ABS, offsetof(struct seccomp_data, arch)),
        BPF_JUMP(BPF_JMP + BPF_JEQ + BPF_K, AUDIT_ARCH_X86_64, 1, 0),
        BPF_STMT(BPF_RET + BPF_K, SECCOMP_RET_KILL_PROCESS),
        BPF_STMT(BPF_LD + BPF_W + BPF_ABS, offsetof(struct seccomp_data, nr)),
        ALLOW(read), ALLOW(write), ALLOW(readv), ALLOW(writev),
        ALLOW(pread64), ALLOW(pwrite64), ALLOW(close), ALLOW(close_range),
        ALLOW(lseek), ALLOW(openat), ALLOW(fstat), ALLOW(newfstatat),
        ALLOW(stat), ALLOW(lstat), ALLOW(statx), ALLOW(access), ALLOW(faccessat),
        ALLOW(faccessat2), ALLOW(readlink), ALLOW(readlinkat),
        ALLOW(getdents64), ALLOW(getcwd), ALLOW(chdir),
        ALLOW(mkdirat), ALLOW(mkdir), ALLOW(unlink), ALLOW(unlinkat),
        ALLOW(rmdir), ALLOW(renameat), ALLOW(renameat2), ALLOW(ftruncate),
        ALLOW(mmap), ALLOW(mprotect), ALLOW(munmap), ALLOW(mremap),
        ALLOW(madvise), ALLOW(brk), ALLOW(arch_prctl),
        ALLOW(futex), ALLOW(set_tid_address), ALLOW(set_robust_list), ALLOW(rseq),
        ALLOW(rt_sigaction), ALLOW(rt_sigprocmask), ALLOW(rt_sigreturn),
        ALLOW(sigaltstack), ALLOW(tgkill), ALLOW(kill),
        ALLOW(getpid), ALLOW(getppid), ALLOW(gettid), ALLOW(getuid),
        ALLOW(geteuid), ALLOW(getgid), ALLOW(getegid),
        ALLOW(clock_gettime), ALLOW(clock_nanosleep), ALLOW(nanosleep),
        ALLOW(gettimeofday), ALLOW(getrandom), ALLOW(sched_yield),
        ALLOW(sched_getaffinity), ALLOW(sched_getparam),
        ALLOW(epoll_create1), ALLOW(epoll_ctl), ALLOW(epoll_pwait),
        ALLOW(epoll_wait), ALLOW(eventfd2), ALLOW(poll), ALLOW(ppoll),
        ALLOW(pipe2), ALLOW(dup), ALLOW(dup2), ALLOW(dup3), ALLOW(fcntl),
        ALLOW(execve), ALLOW(wait4), ALLOW(waitid),
        ALLOW(getrlimit), ALLOW(uname), ALLOW(exit), ALLOW(exit_group),
        /* Query or lower limits for this process only. */
        BPF_JUMP(BPF_JMP + BPF_JEQ + BPF_K, SYS_prlimit64, 0, 4),
        BPF_STMT(BPF_LD + BPF_W + BPF_ABS, offsetof(struct seccomp_data, args[0])),
        BPF_JUMP(BPF_JMP + BPF_JEQ + BPF_K, 0, 0, 1),
        BPF_STMT(BPF_RET + BPF_K, SECCOMP_RET_ALLOW),
        BPF_STMT(BPF_RET + BPF_K, DENY),
        /* Go and libc can create threads and fork, with no namespaces or CLONE_PARENT. */
        BPF_JUMP(BPF_JMP + BPF_JEQ + BPF_K, SYS_clone, 0, 4),
        BPF_STMT(BPF_LD + BPF_W + BPF_ABS, offsetof(struct seccomp_data, args[0])),
        BPF_JUMP(BPF_JMP + BPF_JSET + BPF_K,
                 ~(uint32_t)(0xff | CLONE_VM | CLONE_FS | CLONE_FILES |
                   CLONE_SIGHAND | CLONE_VFORK | CLONE_THREAD | CLONE_SYSVSEM |
                   CLONE_SETTLS | CLONE_PARENT_SETTID | CLONE_CHILD_CLEARTID |
                   CLONE_CHILD_SETTID), 1, 0),
        BPF_STMT(BPF_RET + BPF_K, SECCOMP_RET_ALLOW),
        BPF_STMT(BPF_RET + BPF_K, DENY),
        BPF_STMT(BPF_RET + BPF_K, DENY),
    };
    struct sock_fprog program = {sizeof(filter) / sizeof(filter[0]), filter};
    return syscall(SYS_seccomp, SECCOMP_SET_MODE_FILTER, 0, &program);
}

#define READ_TREE (LANDLOCK_ACCESS_FS_EXECUTE | LANDLOCK_ACCESS_FS_READ_FILE | LANDLOCK_ACCESS_FS_READ_DIR)
#define FS_RIGHTS ((1ULL << 16) - 1)

static int allow_path(int rules, const char *path, uint64_t rights)
{
    int fd = open(path, O_PATH | O_CLOEXEC);
    if (fd < 0)
        return -1;
    struct landlock_path_beneath_attr rule = {.allowed_access = rights, .parent_fd = fd};
    int result = syscall(SYS_landlock_add_rule, rules, LANDLOCK_RULE_PATH_BENEATH, &rule, 0);
    int saved_errno = errno;
    close(fd);
    errno = saved_errno;
    return result;
}

int main(int argc, char **argv)
{
    if (argc < 4)
        return 23;
    struct statfs scratch;
    if (statfs(argv[1], &scratch) || scratch.f_type != TMPFS_MAGIC) {
        fputs("parser sandbox requires tmpfs scratch space\n", stderr);
        return 23;
    }
    /*
     * ABI 6 scopes signals to this sandbox and its descendants. ABI 5
     * restricts device ioctls. A lower ABI cannot enforce this policy.
     */
    if (syscall(SYS_landlock_create_ruleset, NULL, 0, LANDLOCK_CREATE_RULESET_VERSION) < 6) {
        fputs("parser sandbox requires Landlock ABI 6 or newer\n", stderr);
        return 23;
    }
    struct landlock_ruleset_attr policy = {
        .handled_access_fs = FS_RIGHTS,
        .scoped = LANDLOCK_SCOPE_SIGNAL | LANDLOCK_SCOPE_ABSTRACT_UNIX_SOCKET,
    };
    int rules = syscall(SYS_landlock_create_ruleset, &policy, sizeof(policy), 0);
    if (rules < 0 ||
        allow_path(rules, "/usr/bin", READ_TREE) ||
        allow_path(rules, "/usr/lib", READ_TREE) ||
        allow_path(rules, "/usr/share", READ_TREE) ||
        allow_path(rules, "/etc/ld.so.cache", LANDLOCK_ACCESS_FS_READ_FILE) ||
        allow_path(rules, "/dev/null", LANDLOCK_ACCESS_FS_READ_FILE | LANDLOCK_ACCESS_FS_WRITE_FILE) ||
        allow_path(rules, argv[2], LANDLOCK_ACCESS_FS_EXECUTE | LANDLOCK_ACCESS_FS_READ_FILE) ||
        allow_path(rules, argv[1], FS_RIGHTS & ~(LANDLOCK_ACCESS_FS_EXECUTE |
                   LANDLOCK_ACCESS_FS_MAKE_CHAR | LANDLOCK_ACCESS_FS_MAKE_BLOCK |
                   LANDLOCK_ACCESS_FS_MAKE_SOCK | LANDLOCK_ACCESS_FS_MAKE_FIFO |
                   LANDLOCK_ACCESS_FS_IOCTL_DEV)) ||
        prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) ||
        syscall(SYS_landlock_restrict_self, rules, 0)) {
        perror("parser sandbox filesystem policy");
        return 23;
    }
    close(rules);
    const struct rlimit cpu = {20, 20};
    const struct rlimit file = {25U << 20, 25U << 20};
    if (setrlimit(RLIMIT_CPU, &cpu) || setrlimit(RLIMIT_FSIZE, &file)) {
        perror("parser sandbox resource limits");
        return 23;
    }
    if (syscall(SYS_close_range, 3, ~0U, 0) || restrict_syscalls()) {
        perror("parser sandbox syscall policy");
        return 23;
    }
    execv(argv[2], &argv[2]);
    perror("parser sandbox exec");
    return 23;
}
