<p align="center">
  <img src="assets/icon.png" alt="Quillo logo" width="112" height="112">
</p>

<h1 align="center">Quillo</h1>

<p align="center">
  A self-hosted media server for Apple devices, based on Silo.
</p>

---

Quillo is a fork of [Silo](https://github.com/Silo-Server/silo-server). It keeps Silo's
database and API, so it is compatible with Silo apps and an existing Silo install can switch to it
by changing one image line. Quillo is not made, endorsed, or supported by the Silo project or
Silo Media L.L.C.

What Quillo adds is listed in [FORK_NOTES.md](FORK_NOTES.md). Everything else works as Silo
documents it at <https://siloserver.org/docs>.

## Quick start

Quillo images are built on the machine that runs them, for its own CPU (an ARM VPS gets ARM
images). Commands assume the repository root is the cwd.

1. Install Docker, then build the images:

   ```sh
   scripts/quillo-build-image.sh
   ```

2. Create `.env` next to `docker-compose.yml`:

   ```sh
   echo "SECRET_KEY=$(openssl rand -base64 48)" > .env
   echo "MEDIA_ROOT=/path/to/your/media" >> .env
   ```

   Back up `SECRET_KEY` separately; without it, stored credentials can't be decrypted.

3. Start it and open `http://<server>:8090` to create the admin account:

   ```sh
   docker compose up -d
   ```

### Switching from Silo

In your existing Compose file, change the Silo image to `quillo:latest` (one container) or
`quillo:base` (separate PostgreSQL and Redis containers), then `docker compose up -d`. Data,
users, and watch history stay as they are. To go back, change the image line back.

## License

Quillo is free software under the GNU Affero General Public License v3.0 or later, with the
Platform Library Exception in [PLATFORM-EXCEPTION.md](PLATFORM-EXCEPTION.md), the same terms as
Silo. If you run a modified Quillo for other people, you must offer them its source code. See
[LICENSE](LICENSE) and [NOTICE](NOTICE).

"Silo" is a trademark of Silo Media L.L.C. and is used here only to describe what Quillo is
based on and compatible with. See [TRADEMARK.md](TRADEMARK.md).
