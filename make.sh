export GOROOT=/home/charm/Javad/goroot/go
export GOPATH=/home/charm/Javad/gopath
export PATH=$PATH:$GOROOT/bin

export CROSS_COMPILE=x86_64-linux-android-
export ARCH=amd64
export LINUX=/home/charm/Hamid/goldfish_x86
export CGO_ENABLE=0
make -j30

make bin/syz-extract
./bin/syz-extract -arch $ARCH -os "android" -sourcedir "$LINUX" -builddir "$BUILD_DIR" msm_csiphy.txt
./bin/syz-extract -arch $ARCH -os "android" -sourcedir "$LINUX" -builddir "$BUILD_DIR" msm_csid.txt
./bin/syz-extract -arch $ARCH -os "android" -sourcedir "$LINUX" -builddir "$BUILD_DIR" msm_sensor.txt
./bin/syz-extract -arch $ARCH -os "android" -sourcedir "$LINUX" -builddir "$BUILD_DIR" msm_ispif.txt
./bin/syz-extract -arch $ARCH -os "android" -sourcedir "$LINUX" -builddir "$BUILD_DIR" msm_actuator.txt
./bin/syz-extract -arch $ARCH -os "android" -sourcedir "$LINUX" -builddir "$BUILD_DIR" msm_eeprom.txt
./bin/syz-extract -arch $ARCH -os "android" -sourcedir "$LINUX" -builddir "$BUILD_DIR" msm_isp.txt
./bin/syz-extract -arch $ARCH -os "android" -sourcedir "$LINUX" -builddir "$BUILD_DIR" msm_flash.txt
./bin/syz-extract -arch $ARCH -os "android" -sourcedir "$LINUX" -builddir "$BUILD_DIR" msm_cpp.txt
./bin/syz-extract -arch $ARCH -os "android" -sourcedir "$LINUX" -builddir "$BUILD_DIR" msm_probe.txt
make generate
