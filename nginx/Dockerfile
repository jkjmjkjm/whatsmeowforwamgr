FROM nginx:stable

RUN apt-get update && apt-get install -y \
software-properties-common && add-apt-repository "deb http://deb.debian.org/debian stretch-backports main" && \
apt-get update && apt-get install -y \
certbot python-certbot-nginx -t stretch-backports