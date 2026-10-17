# A machine another tuios daemon links to over real ssh, for the e2e tests
# that need the far end of a link to be a different machine and not this
# daemon. See remoteSSHHost in e2e/tui/daemon_upgrade_sshd_test.go.
#
# The image holds only sshd and the account. The test copies a Linux build of
# the checkout to /usr/local/bin/tuios and its public key to the account's
# authorized_keys before it starts the container, so one image serves every
# build and key.
FROM alpine:3.21

RUN apk add --no-cache openssh-server \
 && ssh-keygen -A \
 && adduser -D -s /bin/sh tuios \
 && echo 'tuios:*' | chpasswd -e \
 && install -d -m 700 -o tuios -g tuios /home/tuios/.ssh \
 && printf '%s\n' \
      'PasswordAuthentication no' \
      'KbdInteractiveAuthentication no' \
      'PermitRootLogin no' \
      'AllowUsers tuios' \
      > /etc/ssh/sshd_config.d/e2e.conf

EXPOSE 22
CMD ["/usr/sbin/sshd", "-D", "-e"]
