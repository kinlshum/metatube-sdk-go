FROM python:3.13-alpine
RUN apk add --no-cache ffmpeg
COPY bridge.py /bridge.py
EXPOSE 9210
ENTRYPOINT ["python3", "/bridge.py"]
