# Remi web migration baseline

The Omi web source has been copied into these Remi directories:

- `admin`
- `app`
- `frontend`

The `personas-open-source` directory was excluded because Personas are outside
the Remi MVP. Node modules, build output, Next.js caches, and other generated
artifacts were also excluded.

These web applications still contain Omi branding and Omi service URLs. They
are migration source until Remi branding, API endpoints, authentication, and
the MVP information architecture are applied. Do not treat them as the final
Remi web product yet.

Source: `omi/web`.

