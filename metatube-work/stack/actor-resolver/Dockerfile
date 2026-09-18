FROM python:3.13-alpine
COPY resolver.py /resolver.py
COPY overrides.json /defaults/overrides.json
EXPOSE 9211
ENTRYPOINT ["python3", "/resolver.py"]
