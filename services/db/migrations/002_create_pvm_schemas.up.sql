##
## Accounts
##

create table pvm_accounts
(
    chain_id binary(32)      not null,
    address  binary(20)      not null,

    pubkey   binary(33)      not null,
    balance  bigint unsigned not null
#     nonce   bigint unsigned not null
);
create index pvm_accounts_chain_id ON pvm_accounts (chain_id);
create unique index pvm_accounts_chain_id_address ON pvm_accounts (chain_id, address);

##
## Subnets
##

create table pvm_subnets
(
    id         binary(32)   not null,
    chain_id   binary(32)   not null,
    network_id int unsigned not null,
    threshold  int unsigned not null
);
create unique index pvm_subnets_id_idx ON pvm_subnets (id);

create table pvm_subnet_control_keys
(
    subnet_id binary(32) not null,
    address   binary(20) not null

);
create unique index pvm_subnet_control_keys_subnet_id_address_idx ON pvm_subnet_control_keys (subnet_id, address);

##
## Validators
##

create table pvm_validators
(
    transaction_id binary(32)      not null,

    # Validator
    node_id        binary(32)      not null,
    weight         bigint unsigned not null,

    # Duration validator
    start_time     datetime        not null,
    end_time       datetime        not null,

    # Default subnet validator
    destination    binary(20)      not null,
    shares         bigint unsigned not null,

    # Subnet validator
    subnet_id      binary(32)    not null
);
create index pvm_validators_node_id_idx ON pvm_validators (node_id);
create index pvm_validators_subnet_id_idx ON pvm_validators (subnet_id);
create unique index pvm_validators_tx_id_idx ON pvm_validators (transaction_id);

##
## Chains
##

create table pvm_chains
(
    id             binary(32)       not null,
    subnet_id      binary(32)       not null,
    transaction_id binary(32)       not null,

    name           varchar(255)     not null,
    vm_type        binary(32)       not null,
    genesis_data   varbinary(16384) not null
);
create unique index pvm_chains_id_idx ON pvm_chains (id);

create table pvm_chains_fx_ids
(
    chain_id binary(32) not null,
    fx_id    binary(32) not null

);
create unique index pvm_chains_fx_ids_chain_id_fix_id_idx ON pvm_chains_fx_ids (chain_id, fx_id);

##
## Blocks
##

create table pvm_blocks
(
    id        binary(32) not null,
    parent_id binary(32) not null,
    chain_id  binary(32) not null
);

create table pvm_blocks_transactions
(
    block_id       binary(32) not null,
    transaction_id binary(32) not null
);



##
## Times
##
create table pvm_time_txs
(
    transaction_id binary(32) not null,
    timestamp      datetime   not null

);
create table pvm_transactions
(
    id                      binary(32)       not null,
    chain_id                binary(32)       not null,

    type                    smallint         not null,
    account                 binary(20)       not null,
    amount                  bigint unsigned  not null,
    nonce                   bigint unsigned  not null,
    signature               binary(65),

    canonical_serialization varbinary(16384) not null,
    created_at              timestamp        not null
);
create unique index pvm_transactions_id_idx ON pvm_transactions (id);

