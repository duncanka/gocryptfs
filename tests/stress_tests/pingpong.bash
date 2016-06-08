#!/bin/bash
#
# Mounts two gocryptfs filesystems, "ping" and "pong" and moves the
# linux-3.0 kernel tree back and forth between them.
#
# When called as "pingpong-rsync.bash" it uses "rsync --remove-source-files"
# for moving the files, otherwise plain "mv".

set -eu

cd "$(dirname "$0")"
MD5="$PWD/linux-3.0.md5sums"
MYNAME=$(basename $0)

# Setup
cd /tmp
wget -nv --show-progress -c https://www.kernel.org/pub/linux/kernel/v3.0/linux-3.0.tar.gz

PING=$(mktemp -d ping.XXX)
PONG=$(mktemp -d pong.XXX)
mkdir $PING.mnt $PONG.mnt

# Cleanup trap
# Note: gocryptfs may have already umounted itself because bash relays SIGINT
# Just ignore fusermount errors.
trap "set +e ; cd /tmp; fusermount -u -z $PING.mnt ; fusermount -u -z $PONG.mnt ; rm -rf $PING $PONG $PING.mnt $PONG.mnt" EXIT

gocryptfs -q -init -extpass="echo test" -scryptn=10 $PING
gocryptfs -q -init -extpass="echo test" -scryptn=10 $PONG

gocryptfs -q -extpass="echo test" -nosyslog $PING $PING.mnt
gocryptfs -q -extpass="echo test" -nosyslog $PONG $PONG.mnt

echo "Initial extract"
tar xf /tmp/linux-3.0.tar.gz -C $PING.mnt

function move_and_md5 {
	if [ $MYNAME = pingpong-rsync.bash ]; then
		echo -n "rsync "
		rsync -a --remove-source-files $1 $2
		find $1 -type d -delete
	else
		echo -n "mv "
		mv $1 $2
	fi
	if [ -e $1 ]; then
		echo "error: source directory $1 was not removed"
		exit 1
	fi
	cd $2
	echo -n "md5 "
	md5sum --status -c $MD5
	cd ..
}

N=1
while true; do
	echo -n "$N: "
	move_and_md5 $PING.mnt/linux-3.0 $PONG.mnt
	move_and_md5 $PONG.mnt/linux-3.0 $PING.mnt
	date +%H:%M:%S
	let N=$N+1
done

wait
