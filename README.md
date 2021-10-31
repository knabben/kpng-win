# Windows KPNG Backend

The goal of this backend plugin is port both userspace and kernel space
Kube-proxy Windows modes to a single KPNG module via commands, this module is expected
to connect on KPNG core via GRPC and must be running as an agent on each Windows node
Kube-proxy must be disable in the setup.

## Running

There're two options available `userspace` and `winkernel`, the module should
run as a simple command in the command, for example when compiling from source:

```shell
MODE=userspace make
```

Check kpng core [here](https://github.com/kubernetes-sigs/kpng).