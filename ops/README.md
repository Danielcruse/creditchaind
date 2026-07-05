# CreditChain ops: off-server encrypted backups

Snapshots `state.json`/`blockchain.json` every 15 minutes, encrypts to
your own GPG public key (the private key never touches a production
server), and uploads to a versioned S3 bucket.

## One-time setup (you do this, not me)

### 1. Create the S3 bucket

- Create a bucket (any name/region you like), and **enable versioning**
  on it - this means an accidental bad upload never overwrites the
  last good snapshot.

### 2. Create a scoped IAM user

Create an IAM user with **no console access**, access-key-only, and
attach a policy scoped to just this bucket (replace `BUCKET_NAME`):

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["s3:PutObject", "s3:GetObject", "s3:ListBucket"],
      "Resource": [
        "arn:aws:s3:::BUCKET_NAME",
        "arn:aws:s3:::BUCKET_NAME/*"
      ]
    }
  ]
}
```

Generate an access key for this user. This key can only ever touch
this one bucket - even if a server were fully compromised, the
attacker couldn't reach anything else in your AWS account with it.

### 3. Generate a GPG keypair (keep the private key off any server)

On your own machine, not on either production node:

```
gpg --full-generate-key
gpg --export --armor your-email@example.com > operator_pubkey.asc
```

Send me (or upload to each server yourself) only `operator_pubkey.asc`
- never the private key. Import it on each node:

```
gpg --import operator_pubkey.asc
```

### 4. Fill in the env file on each node

Copy `creditchain-backup.env.example` to `/etc/creditchain-backup.env`
(root-owned, `chmod 600` - it holds the AWS access key) and fill in
the bucket name, a distinct `BACKUP_S3_PREFIX` per node (e.g. `node1`,
`node2`), your GPG key ID, and the IAM access key from step 2.

### 5. Install the systemd units

```
sudo cp backup_state.sh /opt/creditchain/ops/backup_state.sh
sudo chmod +x /opt/creditchain/ops/backup_state.sh
sudo cp creditchain-backup.service creditchain-backup.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now creditchain-backup.timer
```

## Verifying it works

```
sudo systemctl start creditchain-backup.service   # run once immediately
sudo journalctl -u creditchain-backup.service -n 20
aws s3 ls s3://BUCKET_NAME/node1/                 # confirm the object landed
```

To confirm a backup actually restores (do this on your own machine,
where your GPG private key lives):

```
./restore_backup.sh s3://BUCKET_NAME/node1/creditchain-node1-<timestamp>.tar.gz.gpg ./restore-test
cd ../creditchaind-main   # or wherever the built binary is
DATA_DIR=$(pwd)/../ops/restore-test PORT=8099 go run .
```

If the node starts and the chain height matches what you'd expect,
the backup is good.
