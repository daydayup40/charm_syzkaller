export GOROOT=/home/charm/Javad/goroot/go
export GOPATH=/home/charm/Javad/gopath
export PATH=$PATH:$GOROOT/bin

export CROSS_COMPILE=x86_64-linux-android-
export ARCH=amd64
export LINUX=/home/charm/Hamid/goldfish_x86
export CGO_ENABLE=1
make -j30
